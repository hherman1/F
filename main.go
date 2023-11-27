// Copyright 2012 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Watch runs a command each time any file in the current directory is written.
//
// Usage:
//
//	Watch cmd [args...]
//
// Watch opens a new acme window named for the current directory
// with a suffix of /+watch. The window shows the execution of the given
// command. Each time any file in that directory is Put from within acme,
// Watch reexecutes the command and updates the window.
//
// The command and arguments are joined by spaces and passed to rc(1)
// to be interpreted as a shell command line.
//
// The command is printed at the top of the window, preceded by a "% " prompt.
// Changing that line changes the command run each time the window is updated.
// Adding other lines beginning with "% " will cause those commands to be run
// as well.
//
// Executing Quit sends a SIGQUIT on systems that support that signal.
// (Go programs receiving that signal will dump goroutine stacks and exit.)
//
// Executing Kill stops any commands being executed. On Unix it sends the commands
// a SIGINT, followed 100ms later by a SIGTERM, followed 100ms later by a SIGKILL.
// On other systems it sends os.Interrupt followed 100ms later by os.Kill
package main // import "9fans.net/go/acme/Watch"

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"9fans.net/go/acme"
)

var args []string
var win *acme.Win
var needrun = make(chan bool, 1)

func usage() {
	fmt.Fprintf(os.Stderr, "usage: F cmd args...\n")
	os.Exit(2)
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("F: ")
	flag.Usage = usage
	flag.Parse()
	args = flag.Args()

	var err error
	win, err = acme.New()
	if err != nil {
		log.Fatal(err)
	}
	pwd, _ := os.Getwd()
	pwdSlash := strings.TrimSuffix(pwd, "/") + "/"
	win.Name(pwdSlash + "+f")
	win.Ctl("clean")
	win.Ctl("dumpdir " + pwd)
	cmd := "dump F"
	win.Ctl(cmd)
	win.Fprintf("tag", "Kill Quit +NoSuggest %% %s", strings.Join(args, " "))

	needrun <- true
	go events()
	go runner()
	r, err := acme.Log()
	if err != nil {
		log.Fatal(err)
	}
	for {
		_, err := r.Read()
		if err != nil {
			log.Fatal(err)
		}
	}

}

func events() {
	for e := range win.EventChan() {
		switch e.C2 {
		case 'i', 'd':
			select {
			case needrun <- true:
			default:
			}
		case 'x', 'X': // execute
			if string(e.Text) == "Kill" {
				run.Lock()
				cmd := run.cmd
				run.kill = true
				run.Unlock()
				if cmd != nil {
					kill(cmd)
				}
				continue
			}
			if string(e.Text) == "Quit" {
				run.Lock()
				cmd := run.cmd
				run.Unlock()
				if cmd != nil {
					quit(cmd)
				}
				continue
			}
			if string(e.Text) == "Del" {
				win.Ctl("delete")
			}
		}
		win.WriteEvent(e)
	}
	os.Exit(0)
}

var run struct {
	sync.Mutex
	id   int
	cmd  *exec.Cmd
	kill bool
}

func runner() {
	for range needrun {
		run.Lock()
		run.id++
		id := run.id
		lastcmd := run.cmd
		run.cmd = nil
		run.kill = false
		run.Unlock()
		if lastcmd != nil {
			kill(lastcmd)
		}
		lastcmd = nil

		runSetup(id)
		go runBackground(id)
	}
}

func runSetup(id int) {
	// Running synchronously in runner, so no need to watch run.id.
	// reset window
	win.Addr(",")
	win.Write("data", nil)
	win.Addr("#0")
}

func readCmd() (string, error) {
	bs, err := win.ReadAll("tag")
	if err != nil {
		return "", fmt.Errorf("read tag: %w", err)
	}
	_, after, ok := strings.Cut(string(bs), "%")
	if !ok {
		return "", nil
	}
	return strings.TrimSpace(after), nil
}

func runBackground(id int) {
	buf := make([]byte, 4096)
	run.Lock()
	line, err := readCmd()
	if err != nil {
		log.Fatalf("Load command: %v", err)
	}
	run.Unlock()

	// Find the plan9port rc.
	// There may be a different rc in the PATH,
	// but there probably won't be a different 9.
	// Don't just invoke 9, because it will change
	// the PATH.
	var rc string
	if dir := os.Getenv("PLAN9"); dir != "" {
		rc = filepath.Join(dir, "bin/rc")
	} else if nine, err := exec.LookPath("9"); err == nil {
		rc = filepath.Join(filepath.Dir(nine), "rc")
	} else {
		rc = "/usr/local/plan9/bin/rc"
	}

	cmd := exec.Command(rc, "-c", string(line))
	r, w, err := os.Pipe()
	if err != nil {
		log.Fatal(err)
	}
	cmd.Stdout = w
	cmd.Stderr = w
	isolate(cmd)
	err = cmd.Start()
	w.Close()
	run.Lock()
	if run.id != id || run.kill {
		r.Close()
		run.Unlock()
		kill(cmd)
		return
	}
	if err != nil {
		r.Close()
		win.Fprintf("data", "(exec: %s)\n", err)
		run.Unlock()
		return
	}
	run.cmd = cmd
	run.Unlock()
	bol := true
	for {
		n, err := r.Read(buf)
		if err != nil {
			break
		}
		run.Lock()
		if id == run.id && n > 0 {
			p := buf[:n]
			win.Write("data", p)
			bol = p[len(p)-1] == '\n'
		}
		run.Unlock()
	}
	err = cmd.Wait()
	run.Lock()
	if id == run.id {
		// If output was missing final newline, print trailing backslash and add newline.
		if !bol {
			win.Fprintf("data", "\\\n")
		}
		if err != nil {
			win.Fprintf("data", "(%v)\n", err)
		}
	}
	run.Unlock()
}

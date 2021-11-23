/*
 * Copyright (c) 2021 Gilles Chehade <gilles@poolp.org>
 *
 * Permission to use, copy, modify, and distribute this software for any
 * purpose with or without fee is hereby granted, provided that the above
 * copyright notice and this permission notice appear in all copies.
 *
 * THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
 * WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
 * MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
 * ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
 * WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
 * ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
 * OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.
 */

package privsep

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"

	"github.com/poolpOrg/go-ipcmsg"
)

type Privsep struct {
	current   *PrivsepProcess
	processes map[string]*PrivsepProcess
	channels  []PrivsepChannel
}

type PrivsepChannel struct {
	p1 *PrivsepProcess
	p2 *PrivsepProcess
}

type PrivsepProcess struct {
	name string
	main func()

	pid int
	fd  int

	Username   string
	Chrootpath string

	preChrootHandler func() error

	ipcmsg_r chan ipcmsg.IPCMessage
	ipcmsg_w chan ipcmsg.IPCMessage

	r chan ipcmsg.IPCMessage
	w chan ipcmsg.IPCMessage

	ready chan bool

	channels map[string]func(chan ipcmsg.IPCMessage, chan ipcmsg.IPCMessage)
}

const (
	IPCMSG_CHANNEL ipcmsg.IPCMsgType = iota
	IPCMSG_READY   ipcmsg.IPCMsgType = iota
)

var privsepCtx Privsep

// Privsep
func Init() {
	privsepCtx = Privsep{}
	privsepCtx.processes = make(map[string]*PrivsepProcess)
	privsepCtx.channels = make([]PrivsepChannel, 0)
}

func Parent(main func()) *PrivsepProcess {
	parent := PrivsepProcess{}
	parent.name = ""
	parent.main = main
	privsepCtx.processes[parent.name] = &parent
	parent.channels = make(map[string]func(chan ipcmsg.IPCMessage, chan ipcmsg.IPCMessage))
	return &parent
}

func Child(name string, main func()) *PrivsepProcess {
	child := PrivsepProcess{}
	child.name = name
	child.main = main
	privsepCtx.processes[name] = &child
	child.channels = make(map[string]func(chan ipcmsg.IPCMessage, chan ipcmsg.IPCMessage))
	child.ready = make(chan bool)
	return &child
}

func Start() error {
	reexec := os.Getenv("REEXEC")
	privsepCtx.current = privsepCtx.processes[reexec]
	if reexec == "" {
		setup_parent()
	} else {
		setup_child()
	}

	if reexec != "" {
		<-privsepCtx.current.ready
	}
	privsepCtx.current.main()
	return nil
}

func GetParent() *PrivsepProcess {
	return GetProcess("")
}

func GetProcess(name string) *PrivsepProcess {
	return privsepCtx.processes[name]
}

func forkChild(name string) (int, int) {
	binary, err := exec.LookPath(os.Args[0])
	if err != nil {
		log.Fatal(err)
	}

	sp, err := syscall.Socketpair(syscall.AF_LOCAL, syscall.SOCK_STREAM, syscall.AF_UNSPEC)
	if err != nil {
		log.Fatal(err)
	}

	// XXX - not quite there yet
	//syscall.SetNonblock(sp[0], true)
	//syscall.SetNonblock(sp[1], true)

	procAttr := syscall.ProcAttr{}
	procAttr.Files = []uintptr{
		uintptr(syscall.Stdin),
		uintptr(syscall.Stdout),
		uintptr(syscall.Stderr),
		uintptr(sp[0]),
	}
	procAttr.Env = []string{
		fmt.Sprintf("REEXEC=%s", name),
	}

	var pid int

	pid, err = syscall.ForkExec(binary, []string{fmt.Sprintf("%s: %s", os.Args[0], name)}, &procAttr)
	if err != nil {
		log.Fatal(err)
	}

	if syscall.Close(sp[0]) != nil {
		log.Fatal(err)
	}

	return pid, sp[1]
}

func privdrop() {

	if privsepCtx.current.preChrootHandler != nil {
		privsepCtx.current.preChrootHandler()
	}

	if privsepCtx.current.Chrootpath != "" {
		err := syscall.Chroot(privsepCtx.current.Chrootpath)
		if err != nil {
			log.Fatal(err)
		}
		err = syscall.Chdir("/")
		if err != nil {
			log.Fatal(err)
		}
	}

	if privsepCtx.current.Username != "" {
		pw, err := user.Lookup(privsepCtx.current.Username)
		if err != nil {
			log.Fatal(err)
		}

		uid, err := strconv.Atoi(pw.Uid)
		if err != nil {
			log.Fatal(err)
		}

		gid, err := strconv.Atoi(pw.Gid)
		if err != nil {
			log.Fatal(err)
		}

		err = syscall.Setgroups([]int{gid})
		if err != nil {
			log.Fatal(err)
		}

		err = syscall.Setregid(gid, gid)
		if err != nil {
			log.Fatal(err)
		}

		err = syscall.Setreuid(uid, uid)
		if err != nil {
			log.Fatal(err)
		}

	}

}

func parent_dispatcher(r chan ipcmsg.IPCMessage, w chan ipcmsg.IPCMessage) {
	for msg := range r {
		if msg.Fd != -1 {
			syscall.Close(msg.Fd)
		}
	}
}

func child_dispatcher(name string, r chan ipcmsg.IPCMessage, w chan ipcmsg.IPCMessage) {
	for msg := range r {
		switch msg.Hdr.Type {
		case IPCMSG_CHANNEL:
			peer := GetProcess(string(msg.Data))
			log.Printf("[%s] creating channel with %s over fd %d", name, peer.name, msg.Fd)
			r, w := ipcmsg.Channel(os.Getpid(), msg.Fd)
			peer.r = r
			peer.w = w
			go privsepCtx.current.channels[peer.name](r, w)
		case IPCMSG_READY:
			privsepCtx.current.ready <- true
		}
	}
}

func setup_parent() {
	for process := range privsepCtx.processes {
		if process != "" {
			pid, fd := forkChild(process)
			privsepCtx.processes[process].pid = pid
			privsepCtx.processes[process].fd = fd

			// setup ipcmsg channel with child
			r, w := ipcmsg.Channel(pid, fd)
			privsepCtx.processes[process].ipcmsg_r = r
			privsepCtx.processes[process].ipcmsg_w = w
			go parent_dispatcher(r, w)
		}
	}

	setup_channels()

	notify_ready()

	privdrop()
}

func setup_child() {
	parent := privsepCtx.processes[""]
	parent.pid = os.Getppid()
	parent.fd = 3

	// setup ipcmsg channel with parent
	r, w := ipcmsg.Channel(parent.pid, parent.fd)
	privsepCtx.processes[""].ipcmsg_r = r
	privsepCtx.processes[""].ipcmsg_w = w

	go child_dispatcher(privsepCtx.current.name, r, w)

	privdrop()
}

func setup_channels() {
	for _, channel := range privsepCtx.channels {
		log.Println(channel)
		sp, err := syscall.Socketpair(syscall.AF_LOCAL, syscall.SOCK_STREAM, syscall.AF_UNSPEC)
		if err != nil {
			log.Fatal(err)
		}

		p1 := channel.p1
		p2 := channel.p2
		if p1 != privsepCtx.current {
			p1.ipcmsg_w <- ipcmsg.Message(IPCMSG_CHANNEL, []byte(p2.name), sp[0])
		} else {
			p1.r, p1.w = ipcmsg.Channel(os.Getpid(), sp[0])
			go privsepCtx.current.channels[p2.name](p1.r, p1.w)
		}
		if p2 != privsepCtx.current {
			p2.ipcmsg_w <- ipcmsg.Message(IPCMSG_CHANNEL, []byte(p1.name), sp[1])
		} else {
			p2.r, p2.w = ipcmsg.Channel(os.Getpid(), sp[1])
			go privsepCtx.current.channels[p2.name](p2.r, p2.w)
		}
	}
}

func notify_ready() {
	for process := range privsepCtx.processes {
		if process != "" {
			privsepCtx.processes[process].ipcmsg_w <- ipcmsg.Message(IPCMSG_READY, []byte(""), -1)
		}
	}
}

// PrivsepProcess

func (process *PrivsepProcess) Channel(peer *PrivsepProcess, dispatcher func(r chan ipcmsg.IPCMessage, w chan ipcmsg.IPCMessage)) {
	process.channels[peer.name] = dispatcher

	channel := PrivsepChannel{}
	channel.p1 = process
	channel.p2 = peer
	for _, channel := range privsepCtx.channels {
		if channel.p1 == process && channel.p2 == peer ||
			channel.p2 == process && channel.p1 == peer {
			return
		}
	}
	privsepCtx.channels = append(privsepCtx.channels, channel)
}

func (process *PrivsepProcess) Write(msgtype ipcmsg.IPCMsgType, payload []byte, fd int) {
	process.w <- ipcmsg.Message(msgtype, payload, -1)
}

func (process *PrivsepProcess) PreChrootHandler(handler func() error) {
	process.preChrootHandler = handler
}

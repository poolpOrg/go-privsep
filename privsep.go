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
	parent    string
	processes map[string]*PrivsepProcess
}

type PrivsepProcess struct {
	name string
	main func()

	pid int
	fd  int

	Username   string
	Chrootpath string

	preChrootHandler func() error
	preStartHandler  func() error

	privsep_channel *ipcmsg.Channel

	peers    []string
	channels map[string]*ipcmsg.Channel

	ready chan bool
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
}

func newPrivsepProcess(name string, entrypoint func()) *PrivsepProcess {
	process := PrivsepProcess{}
	process.name = name
	process.main = entrypoint
	privsepCtx.processes[name] = &process
	process.peers = make([]string, 0)
	process.channels = make(map[string]*ipcmsg.Channel)
	process.ready = make(chan bool)
	return &process
}

func Parent(name string, main func()) *PrivsepProcess {
	privsepCtx.parent = name
	return newPrivsepProcess(name, main)
}

func Child(name string, main func()) *PrivsepProcess {
	return newPrivsepProcess(name, main)
}

func Start() error {
	reexec := os.Getenv("REEXEC")
	if reexec == "" {
		privsepCtx.current = privsepCtx.processes[privsepCtx.parent]
		setup_parent()
	} else {
		privsepCtx.current = privsepCtx.processes[reexec]
		setup_child(reexec)
	}
	if reexec != GetParent().name {
		<-privsepCtx.current.ready
	}

	if privsepCtx.current.preStartHandler != nil {
		privsepCtx.current.preStartHandler()
	}

	for _, channel := range privsepCtx.current.channels {
		go channel.Dispatch()
	}
	privsepCtx.current.main()
	return nil
}

func GetParent() *PrivsepProcess {
	return GetProcess(privsepCtx.parent)
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

func setup_parent() {
	for process := range privsepCtx.processes {
		if process != "" {
			pid, fd := forkChild(process)
			privsepCtx.processes[process].pid = pid
			privsepCtx.processes[process].fd = fd

			// setup ipcmsg channel with child
			channel := ipcmsg.NewChannel(fmt.Sprintf("%s <-> %s (ipcmsg)", privsepCtx.parent, process), pid, fd)
			privsepCtx.processes[process].privsep_channel = channel
			go channel.Dispatch()
		}
	}

	setup_channels()

	notify_ready()

	privdrop()
}

func setup_child(name string) {
	parent := GetParent()
	parent.pid = os.Getppid()
	parent.fd = 3

	// setup ipcmsg channel with parent
	ipcmsg_channel := ipcmsg.NewChannel(fmt.Sprintf("%s <-> %s (ipcmsg)", name, privsepCtx.parent), parent.pid, parent.fd)

	ipcmsg_channel.Handler(IPCMSG_CHANNEL, func(channel *ipcmsg.Channel, msg ipcmsg.IPCMessage) {
		peer := GetProcess(string(msg.Data))
		privsepCtx.current.channels[peer.name] = ipcmsg.NewChannel(fmt.Sprintf("%s <-> %s", name, peer.name), os.Getpid(), msg.Fd)
	})

	ipcmsg_channel.Handler(IPCMSG_READY, func(channel *ipcmsg.Channel, msg ipcmsg.IPCMessage) {
		privsepCtx.current.ready <- true
	})

	GetParent().privsep_channel = ipcmsg_channel

	go ipcmsg_channel.Dispatch()

	privdrop()
}

func setup_channels() {
	for process := range privsepCtx.processes {
		curProcess := GetProcess(process)
		for _, peer := range curProcess.peers {
			peerProcess := GetProcess(peer)
			match := false
			for _, reversePeer := range peerProcess.peers {
				if reversePeer == curProcess.name {
					match = true
					break
				}
			}
			if !match {
				log.Fatalf("%s has not declared %s as a peer", peerProcess.name, curProcess.name)
			}

			// first, check if a channel already exists
			if _, exists := curProcess.channels[peerProcess.name]; !exists {
				sp, err := syscall.Socketpair(syscall.AF_LOCAL, syscall.SOCK_STREAM, syscall.AF_UNSPEC)
				if err != nil {
					log.Fatal(err)
				}

				if curProcess != privsepCtx.current {
					curProcess.privsep_channel.Message(IPCMSG_CHANNEL, []byte(peerProcess.name), sp[0])
				} else {
					privsepCtx.current.channels[peerProcess.name] = ipcmsg.NewChannel(fmt.Sprintf("%s<->%s", curProcess.name, peerProcess.name), os.Getpid(), sp[0])
				}
				if peerProcess != privsepCtx.current {
					peerProcess.privsep_channel.Message(IPCMSG_CHANNEL, []byte(curProcess.name), sp[1])
				} else {
					privsepCtx.current.channels[curProcess.name] = ipcmsg.NewChannel(fmt.Sprintf("%s<->%s", curProcess.name, peerProcess.name), os.Getpid(), sp[1])
				}
			}
		}
	}
}

func notify_ready() {
	for process := range privsepCtx.processes {
		if process != "" {
			privsepCtx.processes[process].privsep_channel.Message(IPCMSG_READY, []byte(""), -1)
		}
	}
}

// PrivsepProcess
func (process *PrivsepProcess) TalksTo(peers ...string) {
	for _, peer := range peers {
		match := false
		for _, name := range process.peers {
			if name == peer {
				match = true
				break
			}
		}
		if !match {
			process.peers = append(process.peers, peer)
		}
	}
}

func (process *PrivsepProcess) SetHandler(msgtype ipcmsg.IPCMsgType, handler func(*ipcmsg.Channel, ipcmsg.IPCMessage)) {
	privsepCtx.current.channels[process.name].Handler(msgtype, handler)
}

func (process *PrivsepProcess) Message(msgtype ipcmsg.IPCMsgType, payload []byte, fd int) {
	privsepCtx.current.channels[process.name].Message(msgtype, payload, -1)
}

func (process *PrivsepProcess) Query(msgtype ipcmsg.IPCMsgType, payload []byte, fd int) (ipcmsg.IPCMsgType, []byte, int) {
	return privsepCtx.current.channels[process.name].Query(msgtype, payload, -1)
}

func (process *PrivsepProcess) PreChrootHandler(handler func() error) {
	process.preChrootHandler = handler
}

func (process *PrivsepProcess) PreStartHandler(handler func() error) {
	process.preStartHandler = handler
}

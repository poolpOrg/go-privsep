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

package main

import (
	"log"
	"time"

	"github.com/poolpOrg/go-ipcmsg"
	"github.com/poolpOrg/go-privsep"
)

var (
	IPCMSG_PING ipcmsg.IPCMsgType = ipcmsg.NewIPCMsgType(uint64(0))
	IPCMSG_PONG ipcmsg.IPCMsgType = ipcmsg.NewIPCMsgType(uint64(0))
)

func parent_main() {
	<-make(chan bool) // sleep forever
}

func main_foobar() {
	<-make(chan bool)
}

func main_barbaz() {
	foobar := privsep.GetProcess("foobar")

	var seq uint64
	foobar.Message(IPCMSG_PING, seq, -1)
	<-make(chan bool)
}

func ping_handler(msg *ipcmsg.IPCMessage) {
	var seq uint64
	msg.Unmarshal(&seq)

	log.Printf("[%s] received PING with seqid=%d\n", privsep.GetCurrentProcess().Name(), seq)
	time.Sleep(1 * time.Second)
	msg.Reply(IPCMSG_PONG, seq+1, -1)
}

func pong_handler(msg *ipcmsg.IPCMessage) {
	var seq uint64
	msg.Unmarshal(&seq)

	log.Printf("[%s] received PONG with seqid=%d\n", privsep.GetCurrentProcess().Name(), seq)
	time.Sleep(1 * time.Second)
	msg.Reply(IPCMSG_PING, seq+1, -1)
}

func main() {
	privsep.Init()

	privsep.Parent("parent", parent_main)
	privsep.Child("foobar", main_foobar).TalksTo("barbaz")
	privsep.Child("barbaz", main_barbaz).TalksTo("foobar")

	privsep.GetProcess("foobar").PreStartHandler(func() error {
		privsep.GetProcess("barbaz").SetHandler(IPCMSG_PING, ping_handler)
		return nil
	})

	privsep.GetProcess("barbaz").PreStartHandler(func() error {
		privsep.GetProcess("foobar").SetHandler(IPCMSG_PONG, pong_handler)
		return nil
	})

	privsep.Start()
}

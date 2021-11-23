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

	"github.com/poolpOrg/go-ipcmsg"
	"github.com/poolpOrg/go-privsep"
)

const (
	IPCMSG_PING ipcmsg.IPCMsgType = iota
	IPCMSG_PONG ipcmsg.IPCMsgType = iota
)

func parent_main() {
	<-make(chan bool) // sleep forever
}

func main_foobar() {
	barbaz := privsep.GetProcess("barbaz")
	barbaz.SetHandler(IPCMSG_PING, ping_handler)
	<-make(chan bool)
}

func main_barbaz() {
	foobar := privsep.GetProcess("foobar")
	foobar.SetHandler(IPCMSG_PONG, pong_handler)
	foobar.Write(IPCMSG_PING, []byte("test"), -1)
	<-make(chan bool)
}

func ping_handler(channel *ipcmsg.Channel, msg ipcmsg.IPCMessage) {
	log.Printf("[foobar] received PING\n")
	channel.Reply(msg, IPCMSG_PONG, []byte("test"), -1)
}

func pong_handler(channel *ipcmsg.Channel, msg ipcmsg.IPCMessage) {
	log.Printf("[barbaz] received PONG\n")
	channel.Reply(msg, IPCMSG_PING, []byte("test"), -1)
}

func main() {
	privsep.Init()

	_ = privsep.Parent(parent_main)
	foobar := privsep.Child("foobar", main_foobar)
	barbaz := privsep.Child("barbaz", main_barbaz)

	privsep.Channel(barbaz, foobar)

	privsep.Start()
}

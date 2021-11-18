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

	"github.com/poolpOrg/ipcmsg"
	"github.com/poolpOrg/privsep"
)

const (
	IPCMSG_PING ipcmsg.IPCMsgType = iota
	IPCMSG_PONG ipcmsg.IPCMsgType = iota
)

func parent_main() {
	<-make(chan bool) // sleep forever
}

func foobar_main() {
	parent := privsep.GetParent()
	parent.Write(IPCMSG_PING, []byte("abcdef"), -1)
	<-make(chan bool)
}

func parent_dispatcher(r chan ipcmsg.IPCMessage, w chan ipcmsg.IPCMessage) {
	for msg := range r {
		if msg.Hdr.Type == IPCMSG_PING {
			log.Printf("[parent] received PING, sending PONG\n")
			w <- ipcmsg.Message(IPCMSG_PONG, []byte("abcdef"))
		}
	}
}

func foobar_dispatcher(r chan ipcmsg.IPCMessage, w chan ipcmsg.IPCMessage) {
	for msg := range r {
		if msg.Hdr.Type == IPCMSG_PONG {
			log.Printf("[foobar] received PONG, sending PING\n")
			w <- ipcmsg.Message(IPCMSG_PING, []byte("abcdef"))
		}
	}
}

func main() {
	privsep.Init()

	parent := privsep.Parent(parent_main)
	foobar := privsep.Child("foobar", foobar_main)

	parent.Channel(foobar, parent_dispatcher)
	foobar.Channel(parent, foobar_dispatcher)

	privsep.Start()
}

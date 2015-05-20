// Copyright 2013 Joe Walnes and the websocketd team.
// All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package libwebsocketd

import (
	"io"

	"golang.org/x/net/websocket"
)

type SmarthomeWebSocketEndpoint struct {
	ws     *websocket.Conn
	output chan string
	log    *LogScope
	c_type string
}

func NewSmarthomeWebSocketEndpoint(ws *websocket.Conn, log *LogScope) *SmarthomeWebSocketEndpoint {
	return &SmarthomeWebSocketEndpoint{
		ws:     ws,
		output: make(chan string),
		log:    log}
}

func (we *SmarthomeWebSocketEndpoint) Terminate() {
}

func (we *SmarthomeWebSocketEndpoint) Output() chan string {
	return we.output
}

func (we *SmarthomeWebSocketEndpoint) Send(msg string) bool {
	err := websocket.Message.Send(we.ws, msg)
	if err != nil {
		we.log.Trace("websocket", "Cannot send: %s", err)
		return false
	}
	return true
}

func (we *SmarthomeWebSocketEndpoint) StartReading() {
	go we.read_client()
}

func (we *SmarthomeWebSocketEndpoint) read_client() {
	for {
		var msg string
		err := websocket.Message.Receive(we.ws, &msg)
		if err != nil {
			we.log.Debug("limx debug", "smarthome ws receive error: %s", err)
			if err != io.EOF {
				we.log.Debug("websocket", "Cannot receive: %s", err)
			}
			break
		}
		we.output <- msg
	}
	close(we.output)
}

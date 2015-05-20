// Copyright 2013 Joe Walnes and the websocketd team.
// All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/xiangstudio/smarthome-websocketd/libwebsocketd"
)

var logfile string
var logfd *os.File
var logmaxsize int64 = 1024 * 1024 * 50

func log(l *libwebsocketd.LogScope, level libwebsocketd.LogLevel, levelName string, category string, msg string, args ...interface{}) {
	if level < l.MinLevel {
		return
	}
	fullMsg := fmt.Sprintf(msg, args...)

	assocDump := ""
	for index, pair := range l.Associated {
		if index > 0 {
			assocDump += " "
		}
		assocDump += fmt.Sprintf("%s:'%s'", pair.Key, pair.Value)
	}

	if logfile != "" {
		if logfd == nil {
			logfd, _ = os.Create(logfile)
			if logfd == nil {
				return
			}
			l.Mutex.Lock()
			fmt.Fprintf(logfd, "%s | %-6s | %-10s | %s | %s\n", libwebsocketd.Timestamp(), levelName, category, assocDump, fullMsg)
			l.Mutex.Unlock()
		} else {
			stat, _ := logfd.Stat()

			if stat.Size() >= logmaxsize {
				logfd.Close()
				exec.Command("/bin/sh", "-c", `rm -rf logfile`)
				logfd, _ = os.Create(logfile)
				if logfd == nil {
					return
				}
			}

			l.Mutex.Lock()
			fmt.Fprintf(logfd, "%s | %-6s | %-10s | %s | %s\n", libwebsocketd.Timestamp(), levelName, category, assocDump, fullMsg)
			l.Mutex.Unlock()
		}
	} else {
		l.Mutex.Lock()
		fmt.Printf("%s | %-6s | %-10s | %s | %s\n", libwebsocketd.Timestamp(), levelName, category, assocDump, fullMsg)
		l.Mutex.Unlock()
	}
}

// limx debug test
func main() {
	config := parseCommandLine()

	logfile = config.LogFile
	log := libwebsocketd.RootLogScope(config.LogLevel, log)

	if config.DevConsole {
		if config.StaticDir != "" {
			log.Fatal("server", "Invalid parameters: --devconsole cannot be used with --staticdir. Pick one.")
			os.Exit(4)
		}
		if config.CgiDir != "" {
			log.Fatal("server", "Invalid parameters: --devconsole cannot be used with --cgidir. Pick one.")
			os.Exit(4)
		}
	}

	os.Clearenv() // it's ok to wipe it clean, we already read env variables from passenv into config
	handler := libwebsocketd.NewWebsocketdServer(config.Config, log, config.MaxForks)
	http.Handle("/", handler)

	if config.UsingScriptDir {
		log.Info("server", "Serving from directory      : %s", config.ScriptDir)
	} else if config.CommandName != "" {
		log.Info("server", "Serving using application   : %s %s", config.CommandName, strings.Join(config.CommandArgs, " "))
	}
	if config.StaticDir != "" {
		log.Info("server", "Serving static content from : %s", config.StaticDir)
	}
	if config.CgiDir != "" {
		log.Info("server", "Serving CGI scripts from    : %s", config.CgiDir)
	}

	rejects := make(chan error, 1)
	for _, addrSingle := range config.Addr {
		log.Info("server", "Starting WebSocket server   : %s", handler.TellURL("ws", addrSingle, "/"))
		if config.DevConsole {
			log.Info("server", "Developer console enabled   : %s", handler.TellURL("http", addrSingle, "/"))
		} else if config.StaticDir != "" || config.CgiDir != "" {
			log.Info("server", "Serving CGI or static files : %s", handler.TellURL("http", addrSingle, "/"))
		}
		// ListenAndServe is blocking function. Let's run it in
		// go routine, reporting result to control channel.
		// Since it's blocking it'll never return non-error.
		go func(addr string) {
			if config.Ssl {
				rejects <- http.ListenAndServeTLS(addr, config.CertFile, config.KeyFile, nil)
			} else {
				rejects <- http.ListenAndServe(addr, nil)
			}
		}(addrSingle)
	}
	select {
	case err := <-rejects:
		log.Fatal("server", "Can't start server: %s", err)
		os.Exit(3)
	}
}

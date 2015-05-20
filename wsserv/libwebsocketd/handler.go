package libwebsocketd

import (
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/net/websocket"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var ScriptNotFoundError = errors.New("script not found")

// WebsocketdHandler is a single request information and processing structure, it handles WS requests out of all that daemon can handle (static, cgi, devconsole)
type WebsocketdHandler struct {
	server *WebsocketdServer

	Id string
	*RemoteInfo
	*URLInfo // TODO: I cannot find where it's used except in one single place as URLInfo.FilePath
	Env      []string

	command string

	BindSn                         string
	ThisSmarthomeWebSocketEndpoint *SmarthomeWebSocketEndpoint
}

// NewWebsocketdHandler constructs the struct and parses all required things in it...
func NewWebsocketdHandler(s *WebsocketdServer, req *http.Request, log *LogScope) (wsh *WebsocketdHandler, err error) {
	wsh = &WebsocketdHandler{server: s, Id: generateId()}
	log.Associate("id", wsh.Id)

	wsh.RemoteInfo, err = GetRemoteInfo(req.RemoteAddr, s.Config.ReverseLookup)
	if err != nil {
		log.Error("session", "Could not understand remote address '%s': %s", req.RemoteAddr, err)
		return nil, err
	}
	log.Associate("remote", wsh.RemoteInfo.Host)

	wsh.URLInfo, err = GetURLInfo(req.URL.Path, s.Config)
	if err != nil {
		log.Access("session", "NOT FOUND: %s", err)
		return nil, err
	}

	wsh.command = s.Config.CommandName
	if s.Config.UsingScriptDir {
		wsh.command = wsh.URLInfo.FilePath
	}
	//log.Associate("command", wsh.command)

	wsh.Env = createEnv(wsh, req, log)

	return wsh, nil
}

// wshandler returns function that executes code with given log context
func (wsh *WebsocketdHandler) wshandler(log *LogScope) websocket.Handler {
	return websocket.Handler(func(ws *websocket.Conn) {
		wsh.accept(ws, log)
	})
}

func (wsh *WebsocketdHandler) accept(ws *websocket.Conn, log *LogScope) {
	defer func() {
		log.Access("session", "DISCONNECT")
		ws.Close()
		log.Access("session", "wsh.BindSn = %s,thisendpoint = %p", wsh.BindSn, wsh.ThisSmarthomeWebSocketEndpoint)
		for key, forwardEndpoint := range wsh.server.SmarthomeWebSocketEndpointPool {
			log.Debug("before defer ========================== ", ":key =  %s, point = %p ", key, forwardEndpoint)
		}
		if wsh.BindSn != "" && wsh.ThisSmarthomeWebSocketEndpoint == wsh.server.SmarthomeWebSocketEndpointPool[wsh.BindSn] {
			if wsh.server.SmarthomeWebSocketEndpointPool[wsh.BindSn].c_type != "rest" {
				for key, forwardEndpoint := range wsh.server.SmarthomeWebSocketEndpointPool {
					if forwardEndpoint.c_type == "rest" {
						log.Debug("lizm debug find rest ", ":key =  %s, c_type = %s ", key, forwardEndpoint.c_type)
						ws_obj_rsp := make(map[string]interface{})
						data := make(map[string]interface{})
						ws_obj_rsp["type"] = "notification"
						ws_obj_rsp["wsid"] = "1234567890"
						ws_obj_rsp["from"] = wsh.BindSn
						data["msgtype"] = "devicestate"
						data["devicetype"] = wsh.server.SmarthomeWebSocketEndpointPool[wsh.BindSn].c_type
						data["state"] = "offline"
						ws_obj_rsp["data"] = data
						jsonret, _ := json.Marshal(ws_obj_rsp)
						log.Debug("lizm debug", "send state to endpoint: %s", jsonret)
						forwardEndpoint.Send(string(jsonret))
					}
				}
			}
			//if wsh.ThisSmarthomeWebSocketEndpoint == wsh.server.SmarthomeWebSocketEndpointPool[wsh.BindSn] {
			delete(wsh.server.SmarthomeWebSocketEndpointPool, wsh.BindSn)
			//}
		}
		for key, forwardEndpoint := range wsh.server.SmarthomeWebSocketEndpointPool {
			log.Debug("after defer ========================== ", ":key =  %s, point = %p ", key, forwardEndpoint)
		}
	}()

	log.Access("session", "CONNECT")

	if !wsh.server.Config.Smarthome {
		launched, err := launchCmd(wsh.command, wsh.server.Config.CommandArgs, wsh.Env)
		if err != nil {
			log.Error("process", "Could not launch process %s %s (%s)", wsh.command, strings.Join(wsh.server.Config.CommandArgs, " "), err)
			return
		}

		log.Associate("pid", strconv.Itoa(launched.cmd.Process.Pid))

		process := NewProcessEndpoint(launched, log)
		wsEndpoint := NewWebSocketEndpoint(ws, log)

		PipeEndpoints(process, wsEndpoint, log)
	} else {
		smarthomeWebSocketEndpoint := NewSmarthomeWebSocketEndpoint(ws, log)
		smarthomeWebSocketEndpoint.StartReading()
		defer smarthomeWebSocketEndpoint.Terminate()

		for {
			select {
			case msg, ok := <-smarthomeWebSocketEndpoint.Output():
				if !ok {
					return
				}
				log.Debug("limx debug", "receiv from endpoint: %s", msg)

				var jsondata map[string]interface{}
				err := json.Unmarshal([]byte(msg), &jsondata)
				if err != nil {
					log.Debug("limx debug", "json parse error")
					return
				}

				reqtype := jsondata["type"].(string)
				//log.Debug("limx debug", "type: %s", reqtype)

				if reqtype == "auth" {
					mac := jsondata["mac"].(string)
					log.Debug("limx debug", "mac: %s", mac)

					sn := jsondata["sn"].(string)
					log.Debug("limx debug", "sn: %s", sn)

					type Response struct {
						Token string `json:"token"`
					}
					resp := &Response{
						Token: "12345678",
					}
					jsonret, _ := json.Marshal(resp)
					log.Debug("limx debug", "send to endpoint: %s", jsonret)
					smarthomeWebSocketEndpoint.Send(string(jsonret))
				}

				if reqtype == "connect" {
					sn := jsondata["sn"].(string)
					log.Debug("limx debug", "sn: %s", sn)
					token := jsondata["token"].(string)
					log.Debug("limx debug", "token: %s", token)

					c_type := jsondata["c_type"].(string)
					log.Debug("lzm debug", "client type: %s", c_type)
					smarthomeWebSocketEndpoint.c_type = c_type

					wsh.server.SmarthomeWebSocketEndpointPool[sn] = smarthomeWebSocketEndpoint
					log.Debug("lizm debug", "handle.go wsh = %p, SmarthomeWebSocketEndpointPool --- %s", wsh, wsh.server.SmarthomeWebSocketEndpointPool)
					wsh.BindSn = sn
					wsh.ThisSmarthomeWebSocketEndpoint = smarthomeWebSocketEndpoint
					log.Debug("lizm debug", "handle.go wsh = %p, BindSn --- %s, endpoint = %p ", wsh, wsh.BindSn, wsh.ThisSmarthomeWebSocketEndpoint)

					type Response struct {
						Message string `json:"message"`
					}
					resp := &Response{
						Message: "connected",
					}
					jsonret, _ := json.Marshal(resp)
					log.Debug("limx debug", "send to endpoint: %s", jsonret)
					smarthomeWebSocketEndpoint.Send(string(jsonret))

					if c_type != "rest" {
						for key, forwardEndpoint := range wsh.server.SmarthomeWebSocketEndpointPool {
							if forwardEndpoint.c_type == "rest" {
								log.Debug("lizm debug find rest ", ":key =  %s, c_type = %s ", key, forwardEndpoint.c_type)
								ws_obj_rsp := make(map[string]interface{})
								data := make(map[string]interface{})
								ws_obj_rsp["type"] = "notification"
								ws_obj_rsp["wsid"] = "1234567890"
								ws_obj_rsp["from"] = sn
								data["msgtype"] = "devicestate"
								data["devicetype"] = c_type
								data["state"] = "online"
								ws_obj_rsp["data"] = data
								jsonret, _ := json.Marshal(ws_obj_rsp)
								log.Debug("lizm debug", "send state to endpoint: %s", jsonret)
								forwardEndpoint.Send(string(jsonret))
							}
						}
					}
				}

				if reqtype == "rest" {
					//token := jsondata["token"].(string)
					//log.Debug("limx debug", "token: %s", token)

					sn := jsondata["sn"].(string)
					//log.Debug("limx debug", "sn: %s", sn)

					wsid := jsondata["wsid"].(string)
					//log.Debug("limx debug", "wsid: %s", wsid)

					data := jsondata["data"].(map[string]interface{})
					for key, forwardEndpoint := range wsh.server.SmarthomeWebSocketEndpointPool {
						log.Debug("--------------------------------", "lizmmmmmmmmmmmmmmmmmmm --- 888888888888888888888888888888888888 key = %s,type = %s,bindsn = %s", key, forwardEndpoint.c_type, wsh.BindSn)
					}

					forwardEndpoint := wsh.server.SmarthomeWebSocketEndpointPool[sn]
					log.Debug("limx debug", "handle.go forward to endpoint --- %s", forwardEndpoint)

					type Response struct {
						Type string                 `json:"type"`
						Wsid string                 `json:"wsid"`
						From string                 `json:"from"`
						Data map[string]interface{} `json:"data"`
					}
					resp := &Response{
						Type: "rest",
						Wsid: wsid,
						From: wsh.BindSn,
						Data: data,
					}
					jsonret, _ := json.Marshal(resp)
					log.Debug("limx debug", "send to endpoint: %s", jsonret)

					if forwardEndpoint == nil {
						log.Debug("lizm debug", "forwardEndpoint is null, and do not process this request which from rest")
					} else {
						forwardEndpoint.Send(string(jsonret))
					}
				}

				if reqtype == "router" || reqtype == "tv" || reqtype == "cond" {
					wsid := jsondata["wsid"].(string)
					//log.Debug("limx debug", "wsid: %s", wsid)

					from := jsondata["from"].(string)
					//log.Debug("limx debug", "from: %s", from)
					for key, forwardEndpoint := range wsh.server.SmarthomeWebSocketEndpointPool {
						log.Debug("--------------------------------", "lizmmmmmmmmmmmmmmmmmmm --- 999999999999999999999999999999999999  key = %s,type = %s,bindsn = %s", key, forwardEndpoint.c_type, wsh.BindSn)
					}

					forwardEndpoint := wsh.server.SmarthomeWebSocketEndpointPool[from]
					log.Debug("limx debug", "handle.go forward to endpoing --- %s", forwardEndpoint)

					if forwardEndpoint != nil {

						type Response1 struct {
							Type string        `json:"type"`
							Wsid string        `json:"wsid"`
							Data []interface{} `json:"data"`
						}
						resp1 := &Response1{
							Type: "rest",
							Wsid: wsid,
						}
						type Response2 struct {
							Type string                 `json:"type"`
							Wsid string                 `json:"wsid"`
							Data map[string]interface{} `json:"data"`
						}
						resp2 := &Response2{
							Type: "rest",
							Wsid: wsid,
						}
						data1, ok := jsondata["data"].([]interface{})
						if !ok {
							data2 := jsondata["data"].(map[string]interface{})
							resp2.Data = data2
							jsonret, _ := json.Marshal(resp2)
							log.Debug("limx debug", "send to endpoint: %s", jsonret)
							forwardEndpoint.Send(string(jsonret))
						} else {
							resp1.Data = data1
							jsonret, _ := json.Marshal(resp1)
							log.Debug("limx debug", "send to endpoint: %s", jsonret)
							forwardEndpoint.Send(string(jsonret))
						}
					} else {
						log.Debug("lizm debug", "forwardEndpoint is null, and do not process this responce which from other device")
					}
				}

				if reqtype == "notification" {
					from := jsondata["from"].(string)
					log.Debug("lizm debug:", "type: %s", reqtype)
					log.Debug("lizm debug", "from: %s", from)

					for key, forwardEndpoint := range wsh.server.SmarthomeWebSocketEndpointPool {
						if forwardEndpoint.c_type == "rest" {
							log.Debug("lizm debug find rest ", ":key =  %s, c_type = %s ", key, forwardEndpoint.c_type)
							log.Debug("lizm debug", "send notification to endpoint: %s", msg)
							forwardEndpoint.Send(msg)
						}
					}

				}

			}
		}
	}
}

// RemoteInfo holds information about remote http client
type RemoteInfo struct {
	Addr, Host, Port string
}

// GetRemoteInfo creates RemoteInfo structure and fills its fields appropriately
func GetRemoteInfo(remote string, doLookup bool) (*RemoteInfo, error) {
	addr, port, err := net.SplitHostPort(remote)
	if err != nil {
		return nil, err
	}

	var host string
	if doLookup {
		hosts, err := net.LookupAddr(addr)
		if err != nil || len(hosts) == 0 {
			host = addr
		} else {
			host = hosts[0]
		}
	} else {
		host = addr
	}

	return &RemoteInfo{Addr: addr, Host: host, Port: port}, nil
}

// URLInfo - structure carrying information about current request and it's mapping to filesystem
type URLInfo struct {
	ScriptPath string
	PathInfo   string
	FilePath   string
}

// GetURLInfo is a function that parses path and provides URL info according to libwebsocketd.Config fields
func GetURLInfo(path string, config *Config) (*URLInfo, error) {
	if !config.UsingScriptDir {
		return &URLInfo{"/", path, ""}, nil
	}

	parts := strings.Split(path[1:], "/")
	urlInfo := &URLInfo{}

	for i, part := range parts {
		urlInfo.ScriptPath = strings.Join([]string{urlInfo.ScriptPath, part}, "/")
		urlInfo.FilePath = filepath.Join(config.ScriptDir, urlInfo.ScriptPath)
		isLastPart := i == len(parts)-1
		statInfo, err := os.Stat(urlInfo.FilePath)

		// not a valid path
		if err != nil {
			return nil, ScriptNotFoundError
		}

		// at the end of url but is a dir
		if isLastPart && statInfo.IsDir() {
			return nil, ScriptNotFoundError
		}

		// we've hit a dir, carry on looking
		if statInfo.IsDir() {
			continue
		}

		// no extra args
		if isLastPart {
			return urlInfo, nil
		}

		// build path info from extra parts of url
		urlInfo.PathInfo = "/" + strings.Join(parts[i+1:], "/")
		return urlInfo, nil
	}
	panic(fmt.Sprintf("GetURLInfo cannot parse path %#v", path))
}

func generateId() string {
	return strconv.FormatInt(time.Now().UnixNano(), 10)
}

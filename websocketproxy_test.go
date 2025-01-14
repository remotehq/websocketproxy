package websocketproxy

import (
	"bytes"
	"log"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

var (
	serverURL  = "ws://127.0.0.1:7777"
	backendURL = "ws://127.0.0.1:8888"
)

func TestProxy(t *testing.T) {
	// websocket proxy
	supportedSubProtocols := []string{"test-protocol"}
	upgrader := &websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
		Subprotocols: supportedSubProtocols,
	}

	u, _ := url.Parse(backendURL)
	proxy := NewProxy(u, nil)
	proxy.Upgrader = upgrader

	mux := http.NewServeMux()
	mux.Handle("/proxy", proxy)
	server := &http.Server{Addr: ":7777", Handler: mux}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Fatal("ListenAndServe: ", err)
		}
	}()

	time.Sleep(time.Millisecond * 100)

	// backend echo server
	mux2 := http.NewServeMux()
	mux2.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Don't upgrade if original host header isn't preserved
		if r.Host != "127.0.0.1:7777" {
			log.Printf("Host header set incorrectly.  Expecting 127.0.0.1:7777 got %s", r.Host)
			return
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Println(err)
			return
		}

		messageType, p, err := conn.ReadMessage()
		if err != nil {
			return
		}

		if err = conn.WriteMessage(messageType, p); err != nil {
			return
		}
	})
	backendServer := &http.Server{Addr: ":8888", Handler: mux2}
	go func() {
		if err := backendServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Fatal("ListenAndServe: ", err)
		}
	}()

	time.Sleep(time.Millisecond * 100)

	defer server.Close()
	defer backendServer.Close()

	// let's us define two subprotocols, only one is supported by the server
	clientSubProtocols := []string{"test-protocol", "test-notsupported"}
	h := http.Header{}
	for _, subprot := range clientSubProtocols {
		h.Add("Sec-WebSocket-Protocol", subprot)
	}

	// frontend server, dial now our proxy, which will reverse proxy our
	// message to the backend websocket server.
	conn, resp, err := websocket.DefaultDialer.Dial(serverURL+"/proxy", h)
	if err != nil {
		t.Fatal(err)
	}

	// check if the server really accepted only the first one
	in := func(desired string) bool {
		for _, prot := range resp.Header[http.CanonicalHeaderKey("Sec-WebSocket-Protocol")] {
			if desired == prot {
				return true
			}
		}
		return false
	}

	if !in("test-protocol") {
		t.Error("test-protocol should be available")
	}

	if in("test-notsupported") {
		t.Error("test-notsupported should be not recevied from the server.")
	}

	// now write a message and send it to the backend server (which goes trough
	// proxy..)
	msg := "hello kite"
	err = conn.WriteMessage(websocket.TextMessage, []byte(msg))
	if err != nil {
		t.Error(err)
	}

	messageType, p, err := conn.ReadMessage()
	if err != nil {
		t.Error(err)
	}

	if messageType != websocket.TextMessage {
		t.Error("incoming message type is not Text")
	}

	if msg != string(p) {
		t.Errorf("expecting: %s, got: %s", msg, string(p))
	}
}

func TestProxy_ClientPingPong(t *testing.T) {
	// websocket proxy
	supportedSubProtocols := []string{"test-protocol"}
	upgrader := &websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
		Subprotocols: supportedSubProtocols,
	}

	u, _ := url.Parse(backendURL)
	proxy := NewProxy(u, &ProxyConfig{SupportClientPingPong: true})
	proxy.Upgrader = upgrader

	mux := http.NewServeMux()
	mux.Handle("/proxy", proxy)
	server := &http.Server{Addr: ":7777", Handler: mux}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Fatal("ListenAndServe: ", err)
		}
	}()

	time.Sleep(time.Millisecond * 100)

	// backend echo server
	var backendReceivedMessageFromProxy bool
	mux2 := http.NewServeMux()
	mux2.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Don't upgrade if original host header isn't preserved
		if r.Host != "127.0.0.1:7777" {
			log.Printf("Host header set incorrectly.  Expecting 127.0.0.1:7777 got %s", r.Host)
			return
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Println(err)
			return
		}

		_, _, err = conn.ReadMessage()
		if err != nil {
			panic(err)
		}

		backendReceivedMessageFromProxy = true

	})

	backendServer := &http.Server{Addr: ":8888", Handler: mux2}
	go func() {
		if err := backendServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Fatal("ListenAndServe: ", err)
		}
	}()

	time.Sleep(time.Millisecond * 100)

	defer server.Close()
	defer backendServer.Close()

	// let's us define two subprotocols, only one is supported by the server
	clientSubProtocols := []string{"test-protocol", "test-notsupported"}
	h := http.Header{}
	for _, subprot := range clientSubProtocols {
		h.Add("Sec-WebSocket-Protocol", subprot)
	}

	// frontend server, dial now our proxy, which will reverse proxy our
	// message to the backend websocket server.
	conn, _, err := websocket.DefaultDialer.Dial(serverURL+"/proxy", h)
	if err != nil {
		t.Fatal(err)
	}

	// send our ping to the proxy and confirm we get a pong back
	err = conn.WriteMessage(websocket.BinaryMessage, pingMessage)
	if err != nil {
		t.Error(err)
	}

	messageType, p, err := conn.ReadMessage()
	if err != nil {
		t.Error(err)
	}

	if messageType != websocket.BinaryMessage {
		t.Error("returned message type is not binary")
	}

	if !bytes.Equal(p, pongMessage) {
		t.Error("returned message is not pong")
	}

	if backendReceivedMessageFromProxy == true {
		t.Error("ping message was proxied to backend and should not have been")
	}
}

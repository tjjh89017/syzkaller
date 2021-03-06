// Copyright 2016 syzkaller project authors. All rights reserved.
// Use of this source code is governed by Apache 2 LICENSE that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"net/rpc"
	"sync"
	"time"

	. "github.com/google/syzkaller/log"
	. "github.com/google/syzkaller/rpctype"
	"github.com/google/syzkaller/syz-hub/state"
)

var (
	flagConfig = flag.String("config", "", "config file")

	cfg *Config
)

type Config struct {
	Http     string
	Rpc      string
	Workdir  string
	Managers []struct {
		Name string
		Key  string
	}
}

type Hub struct {
	mu   sync.Mutex
	st   *state.State
	keys map[string]string
}

func main() {
	flag.Parse()
	cfg = readConfig(*flagConfig)
	EnableLogCaching(1000, 1<<20)

	st, err := state.Make(cfg.Workdir)
	if err != nil {
		Fatalf("failed to load state: %v", err)
	}
	hub := &Hub{
		st:   st,
		keys: make(map[string]string),
	}
	for _, mgr := range cfg.Managers {
		hub.keys[mgr.Name] = mgr.Key
	}

	hub.initHttp(cfg.Http)

	ln, err := net.Listen("tcp", cfg.Rpc)
	if err != nil {
		Fatalf("failed to listen on %v: %v", cfg.Rpc, err)
	}
	Logf(0, "serving rpc on tcp://%v", ln.Addr())
	s := rpc.NewServer()
	s.Register(hub)
	for {
		conn, err := ln.Accept()
		if err != nil {
			Logf(0, "failed to accept an rpc connection: %v", err)
			continue
		}
		conn.(*net.TCPConn).SetKeepAlive(true)
		conn.(*net.TCPConn).SetKeepAlivePeriod(time.Minute)
		go s.ServeConn(conn)
	}
}

func (hub *Hub) Connect(a *HubConnectArgs, r *int) error {
	if key, ok := hub.keys[a.Name]; !ok || key != a.Key {
		Logf(0, "connect from unauthorized manager %v", a.Name)
		return fmt.Errorf("unauthorized manager")
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()

	Logf(0, "connect from %v: fresh=%v calls=%v corpus=%v", a.Name, a.Fresh, len(a.Calls), len(a.Corpus))
	if err := hub.st.Connect(a.Name, a.Fresh, a.Calls, a.Corpus); err != nil {
		Logf(0, "connect error: %v", err)
		return err
	}
	return nil
}

func (hub *Hub) Sync(a *HubSyncArgs, r *HubSyncRes) error {
	if key, ok := hub.keys[a.Name]; !ok || key != a.Key {
		Logf(0, "sync from unauthorized manager %v", a.Name)
		return fmt.Errorf("unauthorized manager")
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()

	inputs, err := hub.st.Sync(a.Name, a.Add, a.Del)
	if err != nil {
		Logf(0, "sync error: %v", err)
		return err
	}
	r.Inputs = inputs
	Logf(0, "sync from %v: add=%v del=%v new=%v", a.Name, len(a.Add), len(a.Del), len(inputs))
	return nil
}

func readConfig(filename string) *Config {
	if filename == "" {
		Fatalf("supply config in -config flag")
	}
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		Fatalf("failed to read config file: %v", err)
	}
	cfg := new(Config)
	if err := json.Unmarshal(data, cfg); err != nil {
		Fatalf("failed to parse config file: %v", err)
	}
	return cfg
}

package main

import (
	"net/http"
	"net/url"
	"sync/atomic"
)

// atomicConfig provides lock-free atomic Config swap/read.
type atomicConfig struct {
	p atomic.Pointer[Config]
}

func (a *atomicConfig) store(cfg Config) {
	stored := cfg
	a.p.Store(&stored)
}

func (a *atomicConfig) load() Config {
	p := a.p.Load()
	if p == nil {
		return defaultConfig()
	}
	return *p
}

// headerToMap converts an http.Header to a plain map for JSON encoding.
func headerToMap(h http.Header) map[string][]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string][]string, len(h))
	for k, v := range h {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// valuesToMap converts url.Values to a plain map for JSON encoding.
func valuesToMap(v url.Values) map[string][]string {
	if len(v) == 0 {
		return nil
	}
	out := make(map[string][]string, len(v))
	for k, vals := range v {
		out[k] = append([]string(nil), vals...)
	}
	return out
}

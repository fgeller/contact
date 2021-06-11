package main

import (
	"fmt"
	"sync"
	"time"
)

type cache struct {
	sync.RWMutex

	TTL           time.Duration
	ReapIntervals time.Duration

	stop    bool
	entries map[string]int64
}

func newCache(ttl, reapIntervals time.Duration) (*cache, error) {
	if ttl <= 0 {
		return nil, fmt.Errorf("invalid ttl=%#v", ttl)
	}
	if reapIntervals > ttl {
		return nil, fmt.Errorf("reapIntervals should be less than ttl")
	}

	c := &cache{
		TTL:           ttl,
		ReapIntervals: reapIntervals,
		entries:       make(map[string]int64),
	}
	c.startReaper()
	return c, nil
}

func (c *cache) startReaper() {
	go func() {
		for {
			time.Sleep(c.ReapIntervals)
			if c.stop {
				return
			}

			toReap := []string{}
			c.Lock()
			cutOff := time.Now().Add(-c.TTL).UnixNano()
			for v, ts := range c.entries {
				if ts < cutOff {
					toReap = append(toReap, v)
				}
			}
			for _, rv := range toReap {
				delete(c.entries, rv)
			}
			c.Unlock()
		}
	}()
}

func (c *cache) Destroy() {
	c.stop = true
}

func (c *cache) Add(v string) {
	c.Lock()
	c.entries[v] = time.Now().UnixNano()
	c.Unlock()
}

func (c *cache) Size() int {
	var s int
	c.RLock()
	s = len(c.entries)
	c.RUnlock()
	return s
}

func (c *cache) Exists(v string) bool {
	var e bool
	c.RLock()
	_, e = c.entries[v]
	c.RUnlock()
	return e
}

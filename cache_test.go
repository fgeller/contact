package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCacheAddSize(t *testing.T) {
	target := newCache(time.Minute, time.Millisecond)
	defer target.Destroy()

	target.Add("hans")
	require.Equal(t, 1, target.Size())
	require.True(t, target.Exists("hans"), "hans should exist")

	target.Add("schmitt")
	require.Equal(t, 2, target.Size())
	require.True(t, target.Exists("hans"), "hans should exist")
	require.True(t, target.Exists("schmitt"), "schmitt should exist")
}

func TestCacheTTL(t *testing.T) {
	ttl := 10 * time.Millisecond
	reap := time.Millisecond
	target := newCache(ttl, reap)
	defer target.Destroy()

	target.Add("hans")
	require.True(t, target.Exists("hans"), "hans should exist")
	time.Sleep(ttl + 2*reap)
	require.False(t, target.Exists("hans"), "hans should be cleared")
	require.Equal(t, 0, target.Size(), "cache should be empty")
}

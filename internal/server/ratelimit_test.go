package server

import (
	"fmt"
	"testing"
	"time"
)

func TestRateLimiterEvictsTheOldestBucket(t *testing.T) {
	c := &clock{t: time.Unix(0, 0)}
	l := newRateLimiter(0.001, 1, c.Now)
	if ok, _ := l.allow("first"); !ok {
		t.Fatal("first request refused")
	}
	for i := 1; i < maxBuckets; i++ {
		c.Advance(time.Millisecond)
		l.allow(fmt.Sprint("ip", i))
	}
	// A recent client keeps its (empty) bucket when the table is full.
	if ok, _ := l.allow("ip1"); ok {
		t.Fatal("ip1 got a token back")
	}
	c.Advance(time.Millisecond)
	l.allow("newcomer")
	if len(l.buckets) != maxBuckets {
		t.Fatalf("%d buckets", len(l.buckets))
	}
	if _, kept := l.buckets["first"]; kept {
		t.Error("the oldest bucket was kept")
	}
	if _, kept := l.buckets["ip2"]; !kept {
		t.Error("a recent bucket was dropped")
	}
}

func TestLimitKey(t *testing.T) {
	for in, want := range map[string]string{
		"192.168.1.42":         "192.168.1.42",
		"::ffff:192.168.1.42":  "192.168.1.42",
		"2001:db8:1:2:3:4:5:6": "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff::1": "2001:db8:1:2::/64",
		"fe80::1%en0":          "fe80::/64",
		"not an ip":            "not an ip",
	} {
		if got := limitKey(in); got != want {
			t.Errorf("limitKey(%q) = %q, want %q", in, got, want)
		}
	}
}

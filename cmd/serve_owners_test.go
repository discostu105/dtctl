package cmd

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"

	sdkiam "github.com/dynatrace-oss/dtctl/sdk/api/iam"
	"github.com/dynatrace-oss/dtctl/sdk/httpclient"
)

func TestOwnerDirectoryResolvesOncePerUUID(t *testing.T) {
	var asked [][]string
	d := newOwnerDirectory(func(_ context.Context, uuids []string) (map[string]string, error) {
		asked = append(asked, uuids)
		return map[string]string{"u1": "Ada Example"}, nil
	})
	got := d.resolve(context.Background(), []string{"u1", "u2", "u1", ""})
	if got["u1"] != "Ada Example" || len(got) != 1 {
		t.Fatalf("names = %v", got)
	}
	// u2 is unknown to IAM (a service user): remembered, not asked again
	d.resolve(context.Background(), []string{"u1", "u2"})
	if len(asked) != 1 || len(asked[0]) != 2 {
		t.Fatalf("lookups = %v, want one for [u1 u2]", asked)
	}
}

func TestOwnerDirectoryChunks(t *testing.T) {
	var mu sync.Mutex
	var sizes []int
	d := newOwnerDirectory(func(_ context.Context, uuids []string) (map[string]string, error) {
		mu.Lock()
		sizes = append(sizes, len(uuids))
		mu.Unlock()
		return nil, nil
	})
	uuids := make([]string, ownerChunk*2+1)
	for i := range uuids {
		uuids[i] = string(rune('a'+i%26)) + string(rune('0'+i/26))
	}
	d.resolve(context.Background(), uuids)
	sort.Ints(sizes)
	if len(sizes) != 3 || sizes[0] != 1 || sizes[2] != ownerChunk {
		t.Fatalf("chunk sizes = %v", sizes)
	}
}

func TestOwnerDirectoryStopsWithoutPermission(t *testing.T) {
	calls := 0
	d := newOwnerDirectory(func(context.Context, []string) (map[string]string, error) {
		calls++
		return nil, &httpclient.APIError{StatusCode: 403, Message: "Forbidden"}
	})
	if got := d.resolve(context.Background(), []string{"u1"}); len(got) != 0 {
		t.Fatalf("names = %v", got)
	}
	d.resolve(context.Background(), []string{"u2"})
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (no retries after 403)", calls)
	}
}

func TestOwnerDirectoryRetriesAfterTransientError(t *testing.T) {
	fail := true
	d := newOwnerDirectory(func(context.Context, []string) (map[string]string, error) {
		if fail {
			return nil, errors.New("connection reset")
		}
		return map[string]string{"u1": "Ada Example"}, nil
	})
	d.resolve(context.Background(), []string{"u1"})
	fail = false
	if got := d.resolve(context.Background(), []string{"u1"}); got["u1"] != "Ada Example" {
		t.Fatalf("names = %v", got)
	}
}

func TestUserDisplayName(t *testing.T) {
	cases := map[string]sdkiam.User{
		"Ada Example":             {Name: "Ada", Surname: "Example", Email: "ada@example.invalid"},
		"Ada":                     {Name: "Ada"},
		"service@example.invalid": {Email: "service@example.invalid"},
		"":                        {},
	}
	for want, u := range cases {
		if got := userDisplayName(u); got != want {
			t.Errorf("userDisplayName(%+v) = %q, want %q", u, got, want)
		}
	}
}

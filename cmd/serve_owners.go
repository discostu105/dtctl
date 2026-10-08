package cmd

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	sdkiam "github.com/dynatrace-oss/dtctl/sdk/api/iam"
	"github.com/dynatrace-oss/dtctl/sdk/httpclient"
)

// IAM takes at most 25 uuid parameters per request; a few run at once so a
// tenant with hundreds of owners resolves in about a second.
const (
	ownerChunk    = 25
	ownerParallel = 5
)

// userLookup returns the display names IAM knows for some user UUIDs.
type userLookup func(ctx context.Context, uuids []string) (map[string]string, error)

// ownerDirectory resolves document owners (user UUIDs) to people's names for
// `dtctl serve web`, once per tenant and UUID. Listing users needs
// iam:users:read; a token without it leaves owners as UUIDs and the tenant
// stops asking. Any other failure only skips this round.
type ownerDirectory struct {
	lookup userLookup

	mu    sync.Mutex
	names map[string]string // uuid → name; "" = IAM does not know it (service user, deleted)
	off   bool
}

func newOwnerDirectory(lookup userLookup) *ownerDirectory {
	return &ownerDirectory{lookup: lookup, names: map[string]string{}}
}

// resolve returns the names it can find for uuids. It never fails: an owner
// without a name is shown by UUID, as before.
func (d *ownerDirectory) resolve(ctx context.Context, uuids []string) map[string]string {
	d.mu.Lock()
	var missing []string
	seen := map[string]bool{}
	if !d.off {
		for _, u := range uuids {
			if _, ok := d.names[u]; !ok && u != "" && !seen[u] {
				seen[u] = true
				missing = append(missing, u)
			}
		}
	}
	d.mu.Unlock()

	if len(missing) > 0 {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		var wg sync.WaitGroup
		slots := make(chan struct{}, ownerParallel)
		for i := 0; i < len(missing); i += ownerChunk {
			chunk := missing[i:min(i+ownerChunk, len(missing))]
			wg.Add(1)
			slots <- struct{}{}
			go func() {
				defer func() { <-slots; wg.Done() }()
				found, err := d.lookup(ctx, chunk)
				d.mu.Lock()
				defer d.mu.Unlock()
				if err != nil {
					var apiErr *httpclient.APIError
					if errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden) {
						d.off = true
					}
					return // this chunk stays unresolved and is asked again next time
				}
				for _, u := range chunk {
					d.names[u] = found[u]
				}
			}()
		}
		wg.Wait()
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[string]string, len(uuids))
	for _, u := range uuids {
		if n := d.names[u]; n != "" {
			out[u] = n
		}
	}
	return out
}

// iamUserLookup asks the environment's IAM for users by UUID.
func iamUserLookup(h *sdkiam.Handler) userLookup {
	return func(ctx context.Context, uuids []string) (map[string]string, error) {
		res, err := h.ListUsers(ctx, "", uuids, int64(len(uuids)))
		if err != nil {
			return nil, err
		}
		out := make(map[string]string, len(res.Results))
		for _, u := range res.Results {
			out[u.UID] = userDisplayName(u)
		}
		return out, nil
	}
}

// userDisplayName is "Name Surname", else the email, else "".
func userDisplayName(u sdkiam.User) string {
	if n := strings.TrimSpace(u.Name + " " + u.Surname); n != "" {
		return n
	}
	return u.Email
}

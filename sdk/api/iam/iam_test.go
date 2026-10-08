package iam

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dynatrace-oss/dtctl/sdk/httpclient"
)

func newTestClient(t *testing.T, handler http.Handler) *httpclient.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := httpclient.New(srv.URL, httpclient.WithToken("dt0c01.test"))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	return c
}

func TestListUsers(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/platform/iam/v1/organizational-levels/environment/127/users", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		resp := UserListResponse{
			Results: []User{
				{UID: "u-1", Email: "alice@example.invalid", Name: "Alice", Surname: "Test"},
				{UID: "u-2", Email: "bob@example.invalid", Name: "Bob", Surname: "Test"},
			},
			TotalCount: 2,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	h := NewHandler(newTestClient(t, mux))
	result, err := h.ListUsers(context.Background(), "", nil, 0)
	if err != nil {
		t.Fatalf("ListUsers() error: %v", err)
	}
	if len(result.Results) != 2 {
		t.Errorf("got %d users, want 2", len(result.Results))
	}
}

// IAM rejects a comma-joined uuid list ("User uuid is not valid"): each UUID
// is its own query parameter.
func TestListUsersByUUIDRepeatsTheParameter(t *testing.T) {
	var got []string
	mux := http.NewServeMux()
	mux.HandleFunc("/platform/iam/v1/organizational-levels/environment/127/users", func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()["uuid"]
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(UserListResponse{})
	})
	h := NewHandler(newTestClient(t, mux))
	if _, err := h.ListUsers(context.Background(), "", []string{"u-1", "u-2"}, 0); err != nil {
		t.Fatalf("ListUsers() error: %v", err)
	}
	if len(got) != 2 || got[0] != "u-1" || got[1] != "u-2" {
		t.Fatalf("uuid params = %q, want [u-1 u-2]", got)
	}
}

func TestGetUser(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/platform/iam/v1/organizational-levels/environment/127/users/u-1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		resp := User{UID: "u-1", Email: "alice@example.invalid", Name: "Alice"}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	h := NewHandler(newTestClient(t, mux))
	result, err := h.GetUser(context.Background(), "u-1")
	if err != nil {
		t.Fatalf("GetUser() error: %v", err)
	}
	if result.UID != "u-1" {
		t.Errorf("UID = %q, want %q", result.UID, "u-1")
	}
	if result.Email != "alice@example.invalid" {
		t.Errorf("Email = %q, want %q", result.Email, "alice@example.invalid")
	}
}

func TestListGroups(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/platform/iam/v1/organizational-levels/environment/127/groups", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		resp := GroupListResponse{
			Results: []Group{
				{UUID: "g-1", GroupName: "admins", Type: "builtin"},
				{UUID: "g-2", GroupName: "viewers", Type: "custom"},
			},
			TotalCount: 2,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	h := NewHandler(newTestClient(t, mux))
	result, err := h.ListGroups(context.Background(), "", nil, 0)
	if err != nil {
		t.Fatalf("ListGroups() error: %v", err)
	}
	if len(result.Results) != 2 {
		t.Errorf("got %d groups, want 2", len(result.Results))
	}
	if result.Results[0].GroupName != "admins" {
		t.Errorf("first group = %q, want %q", result.Results[0].GroupName, "admins")
	}
}

package vault

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/go-uuid"
	"github.com/hashicorp/vault/logical"
)

type NoopBackend struct {
	sync.Mutex

	Root     []string
	Login    []string
	Paths    []string
	Requests []*logical.Request
	Response *logical.Response

	WrapTTL time.Duration
}

func (n *NoopBackend) HandleRequest(req *logical.Request) (*logical.Response, error) {
	n.Lock()
	defer n.Unlock()

	requestCopy := *req
	n.Paths = append(n.Paths, req.Path)
	n.Requests = append(n.Requests, &requestCopy)
	if req.Storage == nil {
		return nil, fmt.Errorf("missing view")
	}

	if n.Response == nil && (req.WrapTTL != 0 || n.WrapTTL != 0) {
		n.Response = &logical.Response{}
	}

	if n.WrapTTL != 0 {
		n.Response.WrapInfo = &logical.WrapInfo{TTL: n.WrapTTL}
	}

	return n.Response, nil
}

func (n *NoopBackend) HandleExistenceCheck(req *logical.Request) (bool, bool, error) {
	return false, false, nil
}

func (n *NoopBackend) SpecialPaths() *logical.Paths {
	return &logical.Paths{
		Root:            n.Root,
		Unauthenticated: n.Login,
	}
}

func (n *NoopBackend) System() logical.SystemView {
	return logical.StaticSystemView{
		DefaultLeaseTTLVal: time.Hour * 24,
		MaxLeaseTTLVal:     time.Hour * 24 * 30,
	}
}

func (n *NoopBackend) Cleanup() {
	// noop
}

func TestRouter_Mount(t *testing.T) {
	r := NewRouter()
	_, barrier, _ := mockBarrier(t)
	view := NewBarrierView(barrier, "logical/")

	meUUID, err := uuid.GenerateUUID()
	if err != nil {
		t.Fatal(err)
	}
	n := &NoopBackend{}
	err = r.Mount(n, "prod/aws/", &MountEntry{UUID: meUUID}, view)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	meUUID, err = uuid.GenerateUUID()
	if err != nil {
		t.Fatal(err)
	}
	err = r.Mount(n, "prod/aws/", &MountEntry{UUID: meUUID}, view)
	if !strings.Contains(err.Error(), "cannot mount under existing mount") {
		t.Fatalf("err: %v", err)
	}

	if path := r.MatchingMount("prod/aws/foo"); path != "prod/aws/" {
		t.Fatalf("bad: %s", path)
	}

	if v := r.MatchingStorageView("prod/aws/foo"); v != view {
		t.Fatalf("bad: %s", v)
	}

	if path := r.MatchingMount("stage/aws/foo"); path != "" {
		t.Fatalf("bad: %s", path)
	}

	if v := r.MatchingStorageView("stage/aws/foo"); v != nil {
		t.Fatalf("bad: %s", v)
	}

	req := &logical.Request{
		Path: "prod/aws/foo",
	}
	resp, err := r.Route(req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp != nil {
		t.Fatalf("bad: %v", resp)
	}

	// Verify the path
	if len(n.Paths) != 1 || n.Paths[0] != "foo" {
		t.Fatalf("bad: %v", n.Paths)
	}
}

func TestRouter_Unmount(t *testing.T) {
	r := NewRouter()
	_, barrier, _ := mockBarrier(t)
	view := NewBarrierView(barrier, "logical/")

	meUUID, err := uuid.GenerateUUID()
	if err != nil {
		t.Fatal(err)
	}
	n := &NoopBackend{}
	err = r.Mount(n, "prod/aws/", &MountEntry{UUID: meUUID}, view)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	err = r.Unmount("prod/aws/")
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	req := &logical.Request{
		Path: "prod/aws/foo",
	}
	_, err = r.Route(req)
	if !strings.Contains(err.Error(), "unsupported path") {
		t.Fatalf("err: %v", err)
	}
}

func TestRouter_Remount(t *testing.T) {
	r := NewRouter()
	_, barrier, _ := mockBarrier(t)
	view := NewBarrierView(barrier, "logical/")

	meUUID, err := uuid.GenerateUUID()
	if err != nil {
		t.Fatal(err)
	}
	n := &NoopBackend{}
	err = r.Mount(n, "prod/aws/", &MountEntry{UUID: meUUID}, view)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	err = r.Remount("prod/aws/", "stage/aws/")
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	err = r.Remount("prod/aws/", "stage/aws/")
	if !strings.Contains(err.Error(), "no mount at") {
		t.Fatalf("err: %v", err)
	}

	req := &logical.Request{
		Path: "prod/aws/foo",
	}
	_, err = r.Route(req)
	if !strings.Contains(err.Error(), "unsupported path") {
		t.Fatalf("err: %v", err)
	}

	req = &logical.Request{
		Path: "stage/aws/foo",
	}
	_, err = r.Route(req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	// Verify the path
	if len(n.Paths) != 1 || n.Paths[0] != "foo" {
		t.Fatalf("bad: %v", n.Paths)
	}
}

func TestRouter_RootPath(t *testing.T) {
	r := NewRouter()
	_, barrier, _ := mockBarrier(t)
	view := NewBarrierView(barrier, "logical/")

	meUUID, err := uuid.GenerateUUID()
	if err != nil {
		t.Fatal(err)
	}
	n := &NoopBackend{
		Root: []string{
			"root",
			"policy/*",
		},
	}
	err = r.Mount(n, "prod/aws/", &MountEntry{UUID: meUUID}, view)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	type tcase struct {
		path   string
		expect bool
	}
	tcases := []tcase{
		{"random", false},
		{"prod/aws/foo", false},
		{"prod/aws/root", true},
		{"prod/aws/root-more", false},
		{"prod/aws/policy", false},
		{"prod/aws/policy/", true},
		{"prod/aws/policy/ops", true},
	}

	for _, tc := range tcases {
		out := r.RootPath(tc.path)
		if out != tc.expect {
			t.Fatalf("bad: path: %s expect: %v got %v", tc.path, tc.expect, out)
		}
	}
}

func TestRouter_LoginPath(t *testing.T) {
	r := NewRouter()
	_, barrier, _ := mockBarrier(t)
	view := NewBarrierView(barrier, "auth/")

	meUUID, err := uuid.GenerateUUID()
	if err != nil {
		t.Fatal(err)
	}
	n := &NoopBackend{
		Login: []string{
			"login",
			"oauth/*",
		},
	}
	err = r.Mount(n, "auth/foo/", &MountEntry{UUID: meUUID}, view)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	type tcase struct {
		path   string
		expect bool
	}
	tcases := []tcase{
		{"random", false},
		{"auth/foo/bar", false},
		{"auth/foo/login", true},
		{"auth/foo/oauth", false},
		{"auth/foo/oauth/redirect", true},
	}

	for _, tc := range tcases {
		out := r.LoginPath(tc.path)
		if out != tc.expect {
			t.Fatalf("bad: path: %s expect: %v got %v", tc.path, tc.expect, out)
		}
	}
}

func TestRouter_Taint(t *testing.T) {
	r := NewRouter()
	_, barrier, _ := mockBarrier(t)
	view := NewBarrierView(barrier, "logical/")

	meUUID, err := uuid.GenerateUUID()
	if err != nil {
		t.Fatal(err)
	}
	n := &NoopBackend{}
	err = r.Mount(n, "prod/aws/", &MountEntry{UUID: meUUID}, view)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	err = r.Taint("prod/aws/")
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	req := &logical.Request{
		Operation: logical.ReadOperation,
		Path:      "prod/aws/foo",
	}
	_, err = r.Route(req)
	if err.Error() != "unsupported path" {
		t.Fatalf("err: %v", err)
	}

	// Rollback and Revoke should work
	req.Operation = logical.RollbackOperation
	_, err = r.Route(req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	req.Operation = logical.RevokeOperation
	_, err = r.Route(req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
}

func TestRouter_Untaint(t *testing.T) {
	r := NewRouter()
	_, barrier, _ := mockBarrier(t)
	view := NewBarrierView(barrier, "logical/")

	meUUID, err := uuid.GenerateUUID()
	if err != nil {
		t.Fatal(err)
	}
	n := &NoopBackend{}
	err = r.Mount(n, "prod/aws/", &MountEntry{UUID: meUUID}, view)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	err = r.Taint("prod/aws/")
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	err = r.Untaint("prod/aws/")
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	req := &logical.Request{
		Operation: logical.ReadOperation,
		Path:      "prod/aws/foo",
	}
	_, err = r.Route(req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
}

func TestPathsToRadix(t *testing.T) {
	// Provide real paths
	paths := []string{
		"foo",
		"foo/*",
		"sub/bar*",
	}
	r := pathsToRadix(paths)

	raw, ok := r.Get("foo")
	if !ok || raw.(bool) != false {
		t.Fatalf("bad: %v (foo)", raw)
	}

	raw, ok = r.Get("foo/")
	if !ok || raw.(bool) != true {
		t.Fatalf("bad: %v (foo/)", raw)
	}

	raw, ok = r.Get("sub/bar")
	if !ok || raw.(bool) != true {
		t.Fatalf("bad: %v (sub/bar)", raw)
	}
}

func TestRouter_Wrapping(t *testing.T) {
	core, _, root := TestCoreUnsealed(t)

	n := &NoopBackend{}

	core.logicalBackends["noop"] = func(config *logical.BackendConfig) (logical.Backend, error) {
		return n, nil
	}

	meUUID, _ := uuid.GenerateUUID()
	err := core.mount(&MountEntry{
		UUID: meUUID,
		Path: "wraptest",
		Type: "noop",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	// No duration specified
	req := &logical.Request{
		Path:        "wraptest/foo",
		ClientToken: root,
		Operation:   logical.UpdateOperation,
	}
	resp, err := core.HandleRequest(req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp != nil {
		t.Fatalf("bad: %#v", resp)
	}

	// Just in the request
	req = &logical.Request{
		Path:        "wraptest/foo",
		ClientToken: root,
		Operation:   logical.UpdateOperation,
		WrapTTL:     time.Duration(15 * time.Second),
	}
	resp, err = core.HandleRequest(req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp == nil {
		t.Fatalf("bad: %v", resp)
	}
	if resp.WrapInfo == nil || resp.WrapInfo.TTL != time.Duration(15*time.Second) {
		t.Fatalf("bad: %#v", resp)
	}

	// Just in the response
	n.WrapTTL = time.Duration(15 * time.Second)
	req = &logical.Request{
		Path:        "wraptest/foo",
		ClientToken: root,
		Operation:   logical.UpdateOperation,
	}
	resp, err = core.HandleRequest(req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp == nil {
		t.Fatalf("bad: %v", resp)
	}
	if resp.WrapInfo == nil || resp.WrapInfo.TTL != time.Duration(15*time.Second) {
		t.Fatalf("bad: %#v", resp)
	}

	// In both, with request less
	n.WrapTTL = time.Duration(15 * time.Second)
	req = &logical.Request{
		Path:        "wraptest/foo",
		ClientToken: root,
		Operation:   logical.UpdateOperation,
		WrapTTL:     time.Duration(10 * time.Second),
	}
	resp, err = core.HandleRequest(req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp == nil {
		t.Fatalf("bad: %v", resp)
	}
	if resp.WrapInfo == nil || resp.WrapInfo.TTL != time.Duration(10*time.Second) {
		t.Fatalf("bad: %#v", resp)
	}

	// In both, with response less
	n.WrapTTL = time.Duration(10 * time.Second)
	req = &logical.Request{
		Path:        "wraptest/foo",
		ClientToken: root,
		Operation:   logical.UpdateOperation,
		WrapTTL:     time.Duration(15 * time.Second),
	}
	resp, err = core.HandleRequest(req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp == nil {
		t.Fatalf("bad: %v", resp)
	}
	if resp.WrapInfo == nil || resp.WrapInfo.TTL != time.Duration(10*time.Second) {
		t.Fatalf("bad: %#v", resp)
	}
}

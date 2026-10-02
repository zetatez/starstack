package store_test

import (
	"testing"
	"time"

	"github.com/shiyi/starstack/internal/auth"
	"github.com/shiyi/starstack/internal/store"
)

func TestUserLifecycle(t *testing.T) {
	s, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	hash, _ := auth.HashPassword("pw")
	id, err := s.CreateUser("alice", hash, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser("alice", hash, false); err == nil {
		t.Error("duplicate username should fail")
	}
	u, err := s.GetUserByName("alice")
	if err != nil || u.ID != id || !u.IsAdmin {
		t.Errorf("GetUserByName: %+v err=%v", u, err)
	}
	if _, err := s.GetUserByName("nobody"); err != store.ErrNotFound {
		t.Errorf("missing user: want ErrNotFound, got %v", err)
	}
	if err := s.DeleteUser(id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetUserByID(id); err != store.ErrNotFound {
		t.Errorf("deleted user should be gone, got %v", err)
	}
}

func TestShareLimits(t *testing.T) {
	s, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	hash, _ := auth.HashPassword("pw")
	uid, _ := s.CreateUser("bob", hash, false)

	sh := &store.Share{
		Token:     "tok1",
		Path:      "/x.txt",
		Password:  "",
		ExpiresAt: time.Now().Add(time.Hour),
		AllowDown: true,
		MaxUses:   2,
		CreatedBy: uid,
	}
	if err := s.CreateShare(sh); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetShare("tok1")
	if err != nil || got.Path != "/x.txt" {
		t.Fatalf("GetShare: %+v err=%v", got, err)
	}
	_ = s.BumpShareUsed(sh.ID)
	_ = s.BumpShareUsed(sh.ID)
	if err := s.PruneShares(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetShare("tok1"); err != store.ErrNotFound {
		t.Errorf("exhausted share should be pruned, got %v", err)
	}
}

func TestSessionRotation(t *testing.T) {
	s, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	auth.New(s, "test-secret")
	hash, _ := auth.HashPassword("pw")
	uid, _ := s.CreateUser("carol", hash, false)

	exp := time.Now().Add(time.Hour)
	if err := s.SaveSession("h1", uid, exp); err != nil {
		t.Fatal(err)
	}
	if err := s.RotateSession("h1", "h2", uid, exp); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.GetSession("h1"); err != store.ErrNotFound {
		t.Error("old session should be consumed after rotation")
	}
	uID, _, err := s.GetSession("h2")
	if err != nil || uID != uid {
		t.Errorf("rotated session: %d err=%v", uID, err)
	}
}

package db

import (
	"testing"
)

func TestDBLifecycle(t *testing.T) {
	d, err := Open(":memory:", "sqlite3")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	// Seed admin
	admin, err := d.SeedAdmin("admin", "admin@example.com", "1qa2ws")
	if err != nil {
		t.Fatalf("seedadmin: %v", err)
	}
	if !admin.IsAdmin {
		t.Errorf("expected admin to be admin")
	}

	// Duplicate seed returns existing
	admin2, err := d.SeedAdmin("admin", "admin@example.com", "wrong")
	if err != nil {
		t.Fatalf("seedadmin2: %v", err)
	}
	if admin2.ID != admin.ID {
		t.Errorf("expected same admin id after reseed")
	}

	// Verify correct password
	u, err := d.Users.VerifyPassword("admin", "1qa2ws")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if u.LoginName != "admin" {
		t.Errorf("username mismatch: %q", u.LoginName)
	}

	// Verify wrong password fails
	if _, err := d.Users.VerifyPassword("admin", "bad"); err == nil {
		t.Errorf("expected wrong password to fail")
	}

	// Create token + issue JWT
	token, err := IssueToken(admin.ID, TokenExpiry)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if err := d.Users.TokenFor(admin.ID, token); err != nil {
		t.Fatalf("store token: %v", err)
	}
	uid, err := ParseToken(token)
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}
	if uid != admin.ID {
		t.Errorf("uid mismatch: %d vs %d", uid, admin.ID)
	}
	if !d.Users.isValidToken(admin.ID, token) {
		t.Errorf("expected token to be valid")
	}

	// Create non-admin user
	other, err := d.Users.Insert("bob", "bob@example.com", "bobpass", false, false)
	if err != nil {
		t.Fatalf("insert other: %v", err)
	}
	if other.IsAdmin {
		t.Errorf("other should not be admin")
	}

	// User view excludes hash
	views, err := d.Users.UserViews()
	if err != nil {
		t.Fatalf("views: %v", err)
	}
	if len(views) != 2 {
		t.Errorf("expected 2 users, got %d", len(views))
	}
	for _, v := range views {
		if v.LoginName == "" {
			t.Errorf("empty login name in view")
		}
	}

	// Conversations
	c1, err := d.Conversations.CreateConversation(admin.ID, "hello")
	if err != nil {
		t.Fatalf("create conv: %v", err)
	}
	err = d.Conversations.AppendMessage(admin.ID, c1.ID, "user", "hi there", "", []string{"src"}, []any{"ref1"}, nil, "")
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	err = d.Conversations.AppendMessage(admin.ID, c1.ID, "assistant", "hello!", "", nil, nil, nil, "")
	if err != nil {
		t.Fatalf("append2: %v", err)
	}

	convs, err := d.Conversations.ListConversations(admin.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("expected 1 conv, got %d", len(convs))
	}
	if len(convs[0].Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(convs[0].Messages))
	}
	if convs[0].Messages[0].Sources == nil {
		t.Errorf("expected non-nil sources")
	}

	// Get specific conv
	got, err := d.Conversations.GetConversation(admin.ID, convs[0].ID)
	if err != nil {
		t.Fatalf("get conv: %v", err)
	}
	if got.Title != "hello" {
		t.Errorf("title mismatch: %q", got.Title)
	}

	// Other user cannot access admin's conv
	if _, err := d.Conversations.GetConversation(other.ID, convs[0].ID); err == nil {
		t.Errorf("expected other user to be denied")
	}

	// Clear all for admin
	if err := d.Conversations.ClearAllConversations(admin.ID); err != nil {
		t.Fatalf("clear: %v", err)
	}
	convs, _ = d.Conversations.ListConversations(admin.ID)
	if len(convs) != 0 {
		t.Errorf("expected 0 after clear")
	}

	// Delete other user
	if err := d.Users.Delete(other.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	views, _ = d.Users.UserViews()
	if len(views) != 1 {
		t.Errorf("expected 1 user after delete")
	}
}

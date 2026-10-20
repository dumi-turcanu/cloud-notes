package database_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dumi-turcanu/cloud-notes/backend/internal/config"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/database"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/models"
)

// These tests need a real PostgreSQL and run only when TEST_DATABASE_URL is
// set. The target database is WIPED (schema public is recreated).

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		fmt.Println("TEST_DATABASE_URL not set: skipping database integration tests")
		os.Exit(0)
	}
	ctx := context.Background()
	cfg := config.Config{DatabaseURL: dsn, DBMaxConns: 5}

	pool, err := database.NewPool(ctx, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect:", err)
		os.Exit(1)
	}
	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		fmt.Fprintln(os.Stderr, "reset schema:", err)
		os.Exit(1)
	}
	if err := database.Migrate(ctx, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
	testPool = pool
	code := m.Run()
	pool.Close()
	os.Exit(code)
}

func newUser(t *testing.T) *models.UserAccount {
	t.Helper()
	u, err := database.NewUserRepository(testPool).Create(context.Background(),
		fmt.Sprintf("%s@example.com", uuid.NewString()), nil, "$argon2id$placeholder")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestMigrationsAreIdempotentAndSchemaMatchesContract(t *testing.T) {
	ctx := context.Background()
	if err := database.Migrate(ctx, config.Config{DatabaseURL: os.Getenv("TEST_DATABASE_URL")}); err != nil {
		t.Fatalf("second Migrate must be a no-op: %v", err)
	}
	cols := map[string][]string{
		"users":    {"id", "email", "display_name", "password_hash", "failed_login_count", "locked_until", "last_login_at", "created_at", "updated_at"},
		"sessions": {"token_hash", "user_id", "created_at", "last_seen_at", "expires_at", "user_agent", "client_ip"},
		"notes":    {"id", "user_id", "title", "content", "is_public", "share_uuid", "created_at", "updated_at", "deleted_at"},
	}
	for table, want := range cols {
		rows, err := testPool.Query(ctx, `SELECT column_name FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1`, table)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		for rows.Next() {
			var c string
			_ = rows.Scan(&c)
			got[c] = true
		}
		rows.Close()
		if len(got) != len(want) {
			t.Errorf("%s: columns %v, want exactly %v", table, got, want)
		}
		for _, c := range want {
			if !got[c] {
				t.Errorf("%s: missing column %s", table, c)
			}
		}
	}
	var ext int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM pg_extension WHERE extname <> 'plpgsql'`).Scan(&ext); err != nil || ext != 0 {
		t.Errorf("schema must not need extensions (count=%d err=%v)", ext, err)
	}
}

func TestUserCreateGetAndDuplicate(t *testing.T) {
	ctx := context.Background()
	repo := database.NewUserRepository(testPool)
	name := "Ann"
	email := fmt.Sprintf("%s@example.com", uuid.NewString())

	u, err := repo.Create(ctx, email, &name, "hash")
	if err != nil {
		t.Fatal(err)
	}
	if u.ID == uuid.Nil || u.DisplayName == nil || *u.DisplayName != "Ann" || u.CreatedAt.IsZero() {
		t.Errorf("created: %+v", u)
	}
	if _, err := repo.Create(ctx, email, nil, "hash"); !errors.Is(err, models.ErrEmailTaken) {
		t.Errorf("duplicate: %v", err)
	}
	if _, err := repo.Create(ctx, "UPPER@example.com", nil, "h"); err == nil {
		t.Error("CHECK (email = lower(email)) must reject upper-case emails")
	}
	got, err := repo.GetByEmail(ctx, email)
	if err != nil || got.ID != u.ID || got.PasswordHash != "hash" {
		t.Errorf("by email: %+v %v", got, err)
	}
	if got, err := repo.GetByID(ctx, u.ID); err != nil || got.Email != email {
		t.Errorf("by id: %+v %v", got, err)
	}
	if _, err := repo.GetByEmail(ctx, "missing@example.com"); !errors.Is(err, models.ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
	if err := repo.UpdatePasswordHash(ctx, u.ID, "new-hash"); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.GetByID(ctx, u.ID); got.PasswordHash != "new-hash" {
		t.Error("hash not updated")
	}
}

func TestLockoutSQL(t *testing.T) {
	ctx := context.Background()
	repo := database.NewUserRepository(testPool)
	u := newUser(t)

	for i := 1; i <= 2; i++ {
		locked, err := repo.RecordLoginFailure(ctx, u.ID, 3, 10*time.Minute)
		if err != nil || locked != nil {
			t.Fatalf("failure %d: locked=%v err=%v", i, locked, err)
		}
		if got, _ := repo.GetByID(ctx, u.ID); got.FailedLoginCount != i {
			t.Errorf("count after %d failures = %d", i, got.FailedLoginCount)
		}
	}
	before := time.Now()
	locked, err := repo.RecordLoginFailure(ctx, u.ID, 3, 10*time.Minute)
	if err != nil || locked == nil {
		t.Fatalf("3rd failure must lock: %v %v", locked, err)
	}
	if d := locked.Sub(before); d < 9*time.Minute || d > 11*time.Minute {
		t.Errorf("locked_until %v from now, want ~10m", d)
	}
	if got, _ := repo.GetByID(ctx, u.ID); got.FailedLoginCount != 0 || got.LockedUntil == nil {
		t.Errorf("counter must reset on lock: %+v", got)
	}

	if err := repo.RecordLoginSuccess(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.GetByID(ctx, u.ID); got.LockedUntil != nil || got.FailedLoginCount != 0 || got.LastLoginAt == nil {
		t.Errorf("success must clear lock: %+v", got)
	}
	if _, err := repo.RecordLoginFailure(ctx, uuid.New(), 3, time.Minute); !errors.Is(err, models.ErrNotFound) {
		t.Errorf("unknown user: %v", err)
	}
}

func TestLockoutIsAtomicUnderConcurrency(t *testing.T) {
	ctx := context.Background()
	repo := database.NewUserRepository(testPool)
	u := newUser(t)
	const n = 20
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() { _, err := repo.RecordLoginFailure(ctx, u.ID, 1000, time.Minute); errs <- err }()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := repo.GetByID(ctx, u.ID); got.FailedLoginCount != n {
		t.Errorf("lost updates: count=%d want %d", got.FailedLoginCount, n)
	}
}

func TestSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	repo := database.NewSessionRepository(testPool)
	u := newUser(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	idle := time.Hour
	hash := []byte(uuid.NewString())

	err := repo.Create(ctx, models.Session{TokenHash: hash, UserID: u.ID, CreatedAt: now, LastSeenAt: now,
		ExpiresAt: now.Add(12 * time.Hour), UserAgent: "ua", ClientIP: "1.2.3.4"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetActive(ctx, hash, now.Add(time.Minute), idle)
	if err != nil || got.User.ID != u.ID || got.User.Email != u.Email {
		t.Fatalf("active: %+v %v", got, err)
	}

	// Touch is throttled to once a minute.
	if err := repo.Touch(ctx, hash, now.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.GetActive(ctx, hash, now.Add(time.Minute), idle); !got.LastSeenAt.Equal(now) {
		t.Errorf("touch within a minute must be a no-op, last_seen=%v", got.LastSeenAt)
	}
	later := now.Add(90 * time.Second)
	if err := repo.Touch(ctx, hash, later); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.GetActive(ctx, hash, later, idle); !got.LastSeenAt.Equal(later) {
		t.Errorf("touch after a minute must update, last_seen=%v", got.LastSeenAt)
	}

	// Idle and absolute expiry are enforced in SQL.
	if _, err := repo.GetActive(ctx, hash, later.Add(idle+time.Second), idle); !errors.Is(err, models.ErrNotFound) {
		t.Errorf("idle-expired: %v", err)
	}
	if _, err := repo.GetActive(ctx, hash, now.Add(13*time.Hour), 24*time.Hour); !errors.Is(err, models.ErrNotFound) {
		t.Errorf("absolute-expired: %v", err)
	}

	// Cleanup removes only expired sessions.
	live := []byte(uuid.NewString())
	_ = repo.Create(ctx, models.Session{TokenHash: live, UserID: u.ID, CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(100 * time.Hour)})
	deleted, err := repo.DeleteExpired(ctx, now.Add(2*time.Hour), idle)
	if err != nil || deleted < 2 {
		t.Fatalf("DeleteExpired = %d, %v", deleted, err)
	}
	if _, err := repo.GetActive(ctx, live, now, 200*time.Hour); !errors.Is(err, models.ErrNotFound) {
		t.Errorf("idle session should also have been removed: %v", err)
	}

	fresh := []byte(uuid.NewString())
	_ = repo.Create(ctx, models.Session{TokenHash: fresh, UserID: u.ID, CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour)})
	if err := repo.Delete(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	if err := repo.Delete(ctx, fresh); err != nil {
		t.Errorf("delete must be idempotent: %v", err)
	}
	if _, err := repo.GetActive(ctx, fresh, now, idle); !errors.Is(err, models.ErrNotFound) {
		t.Errorf("deleted session still active: %v", err)
	}
}

func TestSessionsCascadeWithUser(t *testing.T) {
	ctx := context.Background()
	u := newUser(t)
	hash := []byte(uuid.NewString())
	now := time.Now()
	repo := database.NewSessionRepository(testPool)
	if err := repo.Create(ctx, models.Session{TokenHash: hash, UserID: u.ID, CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `DELETE FROM users WHERE id = $1`, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetActive(ctx, hash, now, time.Hour); !errors.Is(err, models.ErrNotFound) {
		t.Errorf("session must cascade-delete: %v", err)
	}
}

func TestNotesOwnershipIsolationAndLifecycle(t *testing.T) {
	ctx := context.Background()
	repo := database.NewNoteRepository(testPool)
	ann, bob := newUser(t), newUser(t)

	n := &models.Note{UserID: ann.ID, Title: "Mine", Content: []byte(`{"body":"hello"}`)}
	if err := repo.Create(ctx, n); err != nil {
		t.Fatal(err)
	}
	if n.ID == uuid.Nil || n.ShareUUID == uuid.Nil || n.IsPublic || n.CreatedAt.IsZero() {
		t.Fatalf("create did not return generated fields: %+v", n)
	}

	if got, err := repo.GetByID(ctx, n.ID, ann.ID); err != nil || got == nil || string(got.Content) != `{"body": "hello"}` && string(got.Content) != `{"body":"hello"}` {
		t.Errorf("owner get: %+v %v", got, err)
	}
	// Bob can neither read, change, delete, restore nor publish ann's note.
	if got, _ := repo.GetByID(ctx, n.ID, bob.ID); got != nil {
		t.Error("bob can read ann's note")
	}
	if got, _ := repo.Update(ctx, &models.Note{ID: n.ID, UserID: bob.ID, Title: "x", Content: []byte(`{}`)}); got != nil {
		t.Error("bob can update ann's note")
	}
	if ok, _ := repo.SoftDelete(ctx, n.ID, bob.ID); ok {
		t.Error("bob can delete ann's note")
	}
	if got, _ := repo.Publish(ctx, n.ID, bob.ID); got != nil {
		t.Error("bob can publish ann's note")
	}
	if ok, _ := repo.Unpublish(ctx, n.ID, bob.ID); ok {
		t.Error("bob can unpublish ann's note")
	}
	if l, _ := repo.GetAll(ctx, bob.ID); len(l) != 0 {
		t.Errorf("bob lists %d notes", len(l))
	}

	// Mutations bump updated_at.
	time.Sleep(5 * time.Millisecond)
	upd, err := repo.Update(ctx, &models.Note{ID: n.ID, UserID: ann.ID, Title: "Renamed", Content: []byte(`{"body":"v2"}`)})
	if err != nil || upd == nil || upd.Title != "Renamed" || !upd.UpdatedAt.After(n.UpdatedAt) {
		t.Fatalf("update: %+v %v", upd, err)
	}

	since, err := repo.GetSince(ctx, ann.ID, n.UpdatedAt)
	if err != nil || len(since) != 1 {
		t.Errorf("GetSince: %d %v", len(since), err)
	}
	if l, _ := repo.GetSince(ctx, ann.ID, upd.UpdatedAt); len(l) != 0 {
		t.Errorf("GetSince is strictly greater-than: %d", len(l))
	}

	// Soft delete / trash / restore.
	if ok, err := repo.SoftDelete(ctx, n.ID, ann.ID); err != nil || !ok {
		t.Fatalf("delete: %v %v", ok, err)
	}
	if ok, _ := repo.SoftDelete(ctx, n.ID, ann.ID); ok {
		t.Error("double delete must report not found")
	}
	if l, _ := repo.GetAll(ctx, ann.ID); len(l) != 0 {
		t.Error("deleted note still listed")
	}
	trash, _ := repo.GetTrashed(ctx, ann.ID)
	if len(trash) != 1 || trash[0].DeletedAt == nil {
		t.Fatalf("trash: %+v", trash)
	}
	if got, _ := repo.Restore(ctx, n.ID, bob.ID); got != nil {
		t.Error("bob restored ann's note")
	}
	restored, err := repo.Restore(ctx, n.ID, ann.ID)
	if err != nil || restored == nil || restored.DeletedAt != nil {
		t.Fatalf("restore: %+v %v", restored, err)
	}
	if got, _ := repo.Restore(ctx, n.ID, ann.ID); got != nil {
		t.Error("restoring an active note must report not found")
	}
}

func TestPublicSharingSQL(t *testing.T) {
	ctx := context.Background()
	repo := database.NewNoteRepository(testPool)
	ann := newUser(t)
	n := &models.Note{UserID: ann.ID, Title: "Public one", Content: []byte(`{"body":"x"}`)}
	if err := repo.Create(ctx, n); err != nil {
		t.Fatal(err)
	}

	if got, _ := repo.GetPublic(ctx, n.ShareUUID); got != nil {
		t.Fatal("private note must not be public")
	}
	pub, err := repo.Publish(ctx, n.ID, ann.ID)
	if err != nil || pub == nil || !pub.IsPublic {
		t.Fatalf("publish: %+v %v", pub, err)
	}
	got, err := repo.GetPublic(ctx, n.ShareUUID)
	if err != nil || got == nil || got.Title != "Public one" {
		t.Fatalf("public get: %+v %v", got, err)
	}
	if _, _ = repo.SoftDelete(ctx, n.ID, ann.ID); true {
		if got, _ := repo.GetPublic(ctx, n.ShareUUID); got != nil {
			t.Error("trashed note must not be public")
		}
	}
	_, _ = repo.Restore(ctx, n.ID, ann.ID)
	if ok, err := repo.Unpublish(ctx, n.ID, ann.ID); err != nil || !ok {
		t.Fatalf("unpublish: %v %v", ok, err)
	}
	if got, _ := repo.GetPublic(ctx, n.ShareUUID); got != nil {
		t.Error("unpublished note still public")
	}
}

func TestNotesCascadeWithUser(t *testing.T) {
	ctx := context.Background()
	repo := database.NewNoteRepository(testPool)
	u := newUser(t)
	n := &models.Note{UserID: u.ID, Title: "t", Content: []byte(`{}`)}
	if err := repo.Create(ctx, n); err != nil {
		t.Fatal(err)
	}
	_, _ = testPool.Exec(ctx, `DELETE FROM users WHERE id = $1`, u.ID)
	var c int
	_ = testPool.QueryRow(ctx, `SELECT count(*) FROM notes WHERE id = $1`, n.ID).Scan(&c)
	if c != 0 {
		t.Error("notes must cascade-delete with their user")
	}
}

func TestPoolHonoursMaxConns(t *testing.T) {
	if got := testPool.Config().MaxConns; got != 5 {
		t.Errorf("MaxConns = %d, want 5", got)
	}
}

//go:build integration

package integration_test

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	pgstore "emsim/internal/platform/postgres"
	"github.com/google/uuid"
)

type workerProcess struct {
	cmd    *exec.Cmd
	done   chan error
	waited bool
}

func (p *workerProcess) stop(t *testing.T, crash bool) {
	t.Helper()
	if p.waited {
		return
	}
	var err error
	if crash {
		err = p.cmd.Process.Kill()
	} else {
		err = p.cmd.Process.Signal(syscall.SIGTERM)
	}
	if err != nil {
		t.Fatalf("signal worker: %v", err)
	}
	select {
	case err := <-p.done:
		p.waited = true
		if !crash && err != nil {
			t.Fatalf("graceful worker exit: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not exit")
	}
}

func startWorkerProcess(t *testing.T, binary, databaseURL, role, id string) *workerProcess {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(t.TempDir(), "worker.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	cmd := exec.Command(binary, "worker", "--role="+role)
	cmd.Env = append(os.Environ(),
		"DATABASE_URL="+databaseURL, "WORKER_ID="+id, "WORKER_ADMIN_LISTEN_ADDR="+addr,
		"WORKER_POLL_INTERVAL=50ms", "WORKER_DRAIN_TIMEOUT=100ms",
		"SHORT_CONCURRENCY=1", "LLM_CONCURRENCY=1", "STT_CONCURRENCY=1",
		"WORKER_LOCAL_TEST_POLICY=e2e-fast-v1",
	)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &workerProcess{cmd: cmd, done: make(chan error, 1)}
	go func() { p.done <- cmd.Wait() }()
	t.Cleanup(func() {
		if !p.waited {
			_ = p.cmd.Process.Kill()
			select {
			case <-p.done:
			case <-time.After(10 * time.Second):
				t.Error("worker cleanup timeout")
			}
		}
	})
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case err := <-p.done:
			p.waited = true
			t.Fatalf("worker exited before readiness: %v; log %s", err, log.Name())
		default:
		}
		response, err := client.Get("http://" + addr + "/readyz")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return p
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker readiness timeout; log %s", log.Name())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestWorkerProcessCrashRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	binary := filepath.Join(t.TempDir(), "emsim")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/emsim")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build emsim: %v\n%s", err, output)
	}

	enqueue := func() uuid.UUID {
		t.Helper()
		id := uuid.New()
		if _, err := pool.Exec(ctx, `INSERT INTO tasks (id,kind,scope_type,dedup_key,status,max_attempts,next_attempt_at)
			VALUES ($1,'system.noop','system',$2,'pending',3,clock_timestamp())`, id, "process:"+id.String()); err != nil {
			t.Fatal(err)
		}
		return id
	}
	waitFor := func(label string, query string, args ...any) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			var ready bool
			if err := pool.QueryRow(ctx, query, args...).Scan(&ready); err != nil {
				t.Fatal(err)
			}
			if ready {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("timeout: %s", label)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	worker := startWorkerProcess(t, binary, databaseURL, "worker", "process-first")
	first := enqueue()
	waitFor("real noop handler", "SELECT status='done' AND attempts=1 AND terminal_worker='process-first' FROM tasks WHERE id=$1", first)
	worker.stop(t, false)

	// Hold the actual handler inside its terminal transaction, after claim
	// has committed. Killing the process here must roll back terminal.
	if _, err := pool.Exec(ctx, `
		CREATE FUNCTION process_test_pause() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN PERFORM pg_advisory_xact_lock(739182); RETURN NEW; END $$;
		CREATE TRIGGER process_test_pause BEFORE UPDATE ON tasks
		FOR EACH ROW WHEN (NEW.status='done') EXECUTE FUNCTION process_test_pause();
	`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP TRIGGER IF EXISTS process_test_pause ON tasks; DROP FUNCTION IF EXISTS process_test_pause()")
	})
	blocker, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Release()
	if _, err := blocker.Exec(ctx, "SELECT pg_advisory_lock(739182)"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = blocker.Exec(context.Background(), "SELECT pg_advisory_unlock(739182)") }()
	var blockerPID int
	if err := blocker.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&blockerPID); err != nil {
		t.Fatal(err)
	}
	crashing := startWorkerProcess(t, binary, databaseURL, "worker", "process-crash")
	taskID := enqueue()
	waitFor("terminal transaction in flight", `SELECT EXISTS (
		SELECT FROM pg_stat_activity WHERE datname=current_database() AND $2 = ANY(pg_blocking_pids(pid))
	) AND EXISTS(SELECT FROM tasks WHERE id=$1 AND status='leased' AND leased_worker='process-crash')`, taskID, blockerPID)
	crashing.stop(t, true)
	if _, err := blocker.Exec(ctx, "SELECT pg_advisory_unlock(739182)"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "DROP TRIGGER process_test_pause ON tasks; DROP FUNCTION process_test_pause()"); err != nil {
		t.Fatal(err)
	}

	// Accelerate the production two-minute lease in the fixture, without
	// bypassing the real maintenance process or changing its recovery code.
	if _, err := pool.Exec(ctx, `UPDATE tasks SET lease_started_at=clock_timestamp()-interval '2 minutes',
		lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, taskID); err != nil {
		t.Fatal(err)
	}
	maintenance := startWorkerProcess(t, binary, databaseURL, "maintenance", "process-maintenance")
	waitFor("maintenance requeue", "SELECT status='pending' AND attempts=1 AND lease_token=1 AND last_error_code='lease_expired' FROM tasks WHERE id=$1", taskID)
	maintenance.stop(t, false)
	restarted := startWorkerProcess(t, binary, databaseURL, "worker", "process-restarted")
	waitFor("recovered noop", "SELECT status='done' AND attempts=2 AND lease_token=2 AND terminal_worker='process-restarted' FROM tasks WHERE id=$1", taskID)
	restarted.stop(t, false)
	// A second graceful restart must not execute the terminal task again.
	all := startWorkerProcess(t, binary, databaseURL, "all", "process-all")
	second := enqueue()
	waitFor("all role composition", "SELECT status='done' AND terminal_worker='process-all' FROM tasks WHERE id=$1", second)
	all.stop(t, false)
	var attempts int
	if err := pool.QueryRow(ctx, "SELECT attempts FROM tasks WHERE id=$1", taskID).Scan(&attempts); err != nil || attempts != 2 {
		t.Fatalf("terminal task reran: attempts=%d error=%v", attempts, err)
	}
}

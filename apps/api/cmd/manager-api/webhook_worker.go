package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielfree19/proxydock/apps/api/internal/model"
	"github.com/danielfree19/proxydock/apps/api/internal/store"
)

// webhookPollInterval mirrors the ACME worker. Tight enough that
// publishes feel instant in the UI; loose enough to keep idle CPU
// near zero.
const webhookPollInterval = 2 * time.Second

// runWebhookWorker drains webhook_jobs. Mirrors the ACME worker shape.
// Multiple replicas can share the queue safely because the store's
// claim uses FOR UPDATE SKIP LOCKED on Postgres.
func runWebhookWorker(ctx context.Context, st store.Store, logger *slog.Logger) {
	client := &http.Client{Timeout: 10 * time.Second}
	tick := time.NewTicker(webhookPollInterval)
	defer tick.Stop()
	for {
		for {
			job, hook, err := st.ClaimNextWebhookJob(ctx)
			if err != nil {
				if !errors.Is(err, store.ErrNotFound) {
					logger.Warn("webhook worker: claim failed", "err", err)
				}
				break
			}
			deliverWebhook(ctx, st, logger, client, job, hook)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func deliverWebhook(ctx context.Context, st store.Store, logger *slog.Logger, client *http.Client, job model.WebhookJob, hook model.Webhook) {
	attempts := job.Attempts + 1
	body := []byte(job.Payload)

	req, err := http.NewRequestWithContext(ctx, "POST", hook.URL, bytes.NewReader(body))
	if err != nil {
		finishWebhook(ctx, st, logger, job.ID, "failed", err.Error(), attempts)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "proxydock-webhook/1.0")
	if hook.Secret != "" {
		mac := hmac.New(sha256.New, []byte(hook.Secret))
		mac.Write(body)
		req.Header.Set("X-Webhook-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}

	resp, err := client.Do(req)
	if err != nil {
		retryWebhook(ctx, st, logger, job.ID, fmt.Sprintf("transport: %v", err), attempts)
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		finishWebhook(ctx, st, logger, job.ID, "succeeded", "", attempts)
		return
	}
	retryWebhook(ctx, st, logger, job.ID, fmt.Sprintf("non-2xx: %s", resp.Status), attempts)
}

const maxWebhookAttempts = 5

func retryWebhook(ctx context.Context, st store.Store, logger *slog.Logger, id int64, lastErr string, attempts int) {
	if attempts >= maxWebhookAttempts {
		logger.Info("webhook delivery permanently failed", "job_id", id, "err", lastErr)
		finishWebhook(ctx, st, logger, id, "failed", lastErr, attempts)
		return
	}
	// Exponential backoff: 5s, 25s, 2m5s, 10m25s. Caps before the
	// next attempt would overlap with a typical operator's "they
	// fixed it, retry already" expectation.
	delay := time.Duration(1) * time.Second
	for i := 0; i < attempts; i++ {
		delay *= 5
	}
	if err := st.FinishWebhookJob(ctx, id, "pending", lastErr, time.Now().UTC().Add(delay), attempts); err != nil {
		logger.Warn("webhook retry update failed", "job_id", id, "err", err)
	}
}

func finishWebhook(ctx context.Context, st store.Store, logger *slog.Logger, id int64, status, lastErr string, attempts int) {
	if err := st.FinishWebhookJob(ctx, id, status, lastErr, time.Now().UTC(), attempts); err != nil {
		logger.Warn("webhook finish update failed", "job_id", id, "err", err)
	}
}

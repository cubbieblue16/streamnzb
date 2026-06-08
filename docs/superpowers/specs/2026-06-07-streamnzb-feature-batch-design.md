# streamnzb feature batch — design (2026-06-07)

Four features on branch `wwe-date-based-search`. Built in risk order: 5 → 3 → 4 → 2.
Each ships with unit tests; PAR2 is feature-gated OFF by default so it cannot affect the
running addon until explicitly enabled.

## Feature 5 — quota-aware indexer rotation

**Problem:** searches/grabs can fire at an indexer whose daily API-hit or download quota is
already exhausted, wasting the attempt (the newznab client returns a limit error).

**Approach:** filter at the aggregator. Each `Indexer.GetUsage()` exposes
`APIHitsLimit/Remaining` and `DownloadsLimit/Remaining`. The day-boundary reset already
happens in `UsageManager.GetIndexerUsage`, so an exhausted indexer auto-resumes next day with
no extra code.

- `hasAPIHeadroom(idx)` / `hasDownloadHeadroom(idx)`: a limit of `<=0` means unknown/unlimited
  → headroom present; otherwise `Remaining > 0`.
- `eligibleIndexers(indexers, quotaAware, forDownload)`: drop exhausted ones, **preserving the
  user's configured priority order** among those with headroom. If *all* are exhausted, return
  the full list unchanged (graceful fallback — better to try and get a limit error than return
  nothing).
- `searchCombined`, `searchWithFailover`, `DownloadNZB` iterate over the eligible set.
- Config: `indexer_quota_aware *bool` (default true via `QuotaAwareEnabled()`). `Aggregator`
  gets a `QuotaAware bool` field set from config in bootstrap.

**Risk:** zero eligible indexers → handled by the all-exhausted fallback.

## Feature 3 — stream label enrichment

**Problem:** Stremio stream rows show a bare `StreamNZB`; user can't tell releases apart.

**Approach:** formatting-only change in `pkg/server/stremio/handlers_request.go`
(`buildStreamsFromPlaylist`). Reuse the existing scene parser (`pkg/search/parser`, fields
Resolution/Quality/Codec/HDR/Audio/Channels/Group/BitDepth) and the rich-format pattern in
`metadata.go`. All release metadata (`cand.Metadata`, `cand.Release.Size/PubDate/Indexer`,
`list.CachedAvailable[DetailsURL]`) is already in scope at build time.

- New helpers `buildEnrichedStreamName` / `buildEnrichedStreamDescription`.
- Name: `2160p BluRay x265` (+ availNZB marker). Description lines: quality • codec • HDR •
  audio; size • age • group; availability; raw release title.
- Config: per-stream / global `stream_label_format` ("detailed" default, "minimal" = old
  behavior). Null-safe (parser never returns nil).

## Feature 4 — customizable alerting

**Problem:** no proactive notification when something breaks; events are inline log calls only.

**Approach:** package-level notifier singleton (`pkg/services/notifier`) with a buffered queue +
worker goroutine; non-blocking `Emit` (drops on full queue). User chooses which events fire and
which channels receive them.

- Events: `provider_auth_fail`, `provider_down`, `indexer_quota`, `playback_failure`,
  `failover_exhausted`.
- Channels: generic webhook (JSON POST), ntfy (topic URL), Discord (webhook embed). Each
  channel lists which event types it wants; per-event master toggle too.
- Config: `notifier { enabled, events{type:bool}, min_interval_seconds, channels[] }` with env
  overrides; per-channel dedupe/rate-limit to avoid alert storms.
- Hook points (emit, nil-safe singleton): NNTP auth fail (`pkg/usenet/nntp/pool.go`), providers
  exhausted (`pkg/usenet/pool` `ErrNoProvidersAvailable`), indexer quota
  (`pkg/indexer/newznab` limit checks), playback failure + "No more fallback slots"
  (`handlers_playback.go`).
- Bootstrap constructs the notifier and exposes it; a package singleton lets event sites emit
  without threading a dependency through every call site.
- Frontend: new `NotificationsSection.jsx` tab (per-event toggles, channel rows, test-send
  button) following `AdvancedSettingsSection.jsx`.
- API: `POST /api/notifier/test` sends a synthetic event to verify channel config.

## Feature 2 — PAR2 download-repair-serve (gated OFF)

**Problem:** ~39 releases in a single live-log window fail with "likely requires PAR2 repair"
and are discarded.

**Decision:** streaming block-recovery is architecturally wrong (would stall byte-range reads);
use a **download-repair-serve fallback slot**. When normal playback slots are exhausted and the
NZB carries `.par2` recovery files and the feature is enabled, download the fileset + recovery
to a temp dir, run the `par2` binary to repair, then serve the repaired media file as a
last-resort slot.

- New `pkg/services/par2`: `DetectPar2Files(nzb)`, `Repair(ctx, workDir, files, par2Files)`
  shelling out to `par2 repair` (Alpine pkg `par2cmdline`; added to Dockerfile).
- Config (all gated by `par2_enable=false` default): `par2_binary_path` (default "par2"),
  `par2_work_dir`, `par2_max_concurrent` (default 1), `par2_timeout_minutes` (default 30),
  `par2_max_bytes` (disk cap; skip if fileset exceeds it).
- Integrated as a synthetic last-resort failover slot in the playback path; background repair
  with status; temp dir cleaned after retention/session close.
- Tests use a fake `par2` script (success/failure) so no network or real binary needed in CI.

**Risks:** disk usage (bounded by `par2_max_bytes`), repair latency (minutes), binary dep
(Dockerfile). All mitigated by default-off gating + caps.

## Testing & ship

- Unit + stress tests per feature (notifier queue/fan-out under churn, quota skip/order, parser
  formatter, par2 orchestration with fake binary). Full `go test -race ./...`. Frontend build.
- Commit, push `wwe-date-based-search` to `cubbieblue16` fork, wait for GH Actions image,
  redeploy Portainer stack 20 with pullImage, verify manifest + smoke test.

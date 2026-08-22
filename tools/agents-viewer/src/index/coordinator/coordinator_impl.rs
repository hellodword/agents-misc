use super::*;

impl IndexCoordinator {
    #[must_use]
    pub fn new(
        database: Database,
        writer: WriterHandle,
        roots: SourceRoots,
        max_event_bytes: usize,
        policy: InitialIndexPolicy,
    ) -> Self {
        let (commands, command_receiver) = mpsc::channel(DIRECT_SYNC_QUEUE_CAPACITY);
        Self {
            database,
            writer,
            roots,
            max_event_bytes,
            policy,
            generation: Arc::new(AtomicU64::new(0)),
            io_gate: IoGate::new(),
            shared: Arc::new(RwLock::new(SharedState::default())),
            commands,
            command_receiver: Arc::new(Mutex::new(Some(command_receiver))),
        }
    }

    #[must_use]
    pub fn handle(&self) -> CoordinatorHandle {
        CoordinatorHandle {
            shared: Arc::clone(&self.shared),
            commands: self.commands.clone(),
        }
    }

    pub async fn reconcile(&self) -> Result<ReconcileReport> {
        self.reconcile_mode(&CancellationToken::new(), None, false)
            .await
    }

    pub async fn reconcile_with_updates(
        &self,
        shutdown: &CancellationToken,
        updates: Option<&mpsc::Sender<IndexUpdate>>,
    ) -> Result<ReconcileReport> {
        self.reconcile_mode(shutdown, updates, true).await
    }

    pub(super) async fn reconcile_mode(
        &self,
        shutdown: &CancellationToken,
        updates: Option<&mpsc::Sender<IndexUpdate>>,
        foreground: bool,
    ) -> Result<ReconcileReport> {
        let generation = self.next_generation();
        if foreground {
            send_update(updates, IndexUpdate::Discovering { generation }).await;
        }
        let now_micros = chrono::Utc::now().timestamp_micros();
        let discovery = self
            .discover(generation, now_micros, shutdown.clone())
            .await?;
        let stored = load_stored_sources(&self.database).await?;
        let stored_by_key = stored
            .iter()
            .cloned()
            .map(|source| (stored_key(source.root_kind, &source.relative_path), source))
            .collect::<HashMap<_, _>>();
        let discovered_keys = discovery
            .sources
            .iter()
            .map(source_key)
            .collect::<HashSet<_>>();
        let mut missing = Vec::new();
        if discovery.issues.is_empty() {
            for source in &stored {
                if !discovered_keys.contains(&stored_key(source.root_kind, &source.relative_path))
                    && source.scan_state != "source_missing"
                {
                    missing.push((source.root_kind, source.relative_path.clone()));
                }
            }
        }
        let mut present = Vec::new();
        let mut changed = Vec::new();
        let mut processed_files = 0_u64;
        let mut processed_bytes = 0_u64;
        let mut excluded_files = 0_u64;
        let mut excluded_bytes = 0_u64;
        for source in &discovery.sources {
            let automatic = automatic_priority(self.policy, source, now_micros);
            let Some(_priority) = automatic else {
                excluded_files = excluded_files.saturating_add(1);
                excluded_bytes = excluded_bytes.saturating_add(source.source.size_bytes);
                continue;
            };
            // Explicit reconciliation and atomic rebuild complete the foreground window. Older
            // history remains the long-lived runtime scheduler's responsibility.
            if automatic == Some(WorkPriority::Background) {
                excluded_files = excluded_files.saturating_add(1);
                excluded_bytes = excluded_bytes.saturating_add(source.source.size_bytes);
                continue;
            }
            match stored_by_key.get(&source_key(source)) {
                Some(stored) if metadata_matches(stored, source) => {
                    if stored.scan_state == "source_missing" {
                        present.push((stored.root_kind, stored.relative_path.clone()));
                    }
                    processed_files = processed_files.saturating_add(1);
                    processed_bytes = processed_bytes.saturating_add(source.source.size_bytes);
                }
                _ => changed.push(source.clone()),
            }
        }
        self.writer.mark_sources_missing(missing.clone()).await?;
        self.writer.mark_sources_present(present).await?;
        let mut progress = IndexProgress {
            total_files: processed_files.saturating_add(changed.len() as u64),
            processed_files,
            total_bytes: processed_bytes
                .saturating_add(changed.iter().map(|source| source.source.size_bytes).sum()),
            processed_bytes,
            failed_files: 0,
            excluded_files,
            excluded_bytes,
        };
        if foreground {
            send_update(
                updates,
                IndexUpdate::Progress {
                    generation,
                    progress: progress.clone(),
                },
            )
            .await;
        }
        let mut report = ReconcileReport {
            generation,
            discovered_files: discovery.sources.len() as u64,
            discovered_bytes: discovery.total_bytes,
            removed_files: missing.len() as u64,
            discovery_issues: discovery.issues.len() as u64,
            excluded_files,
            excluded_bytes,
            reconcile_again: !discovery.issues.is_empty(),
            ..ReconcileReport::default()
        };
        let gate = self.io_gate.clone();
        let writer = self.writer.clone();
        let database = self.database.clone();
        let max_event_bytes = self.max_event_bytes;
        let mut results = stream::iter(changed.into_iter().map(|source| {
            let writer = writer.clone();
            let database = database.clone();
            let scan_shutdown = shutdown.clone();
            let lease = gate.register(WorkPriority::Recent);
            let bytes = source.source.size_bytes;
            async move {
                let result = match lease {
                    Ok(lease) => {
                        scan_source_with_lease(
                            database,
                            writer,
                            source,
                            max_event_bytes,
                            now_micros,
                            scan_shutdown,
                            lease,
                        )
                        .await
                    }
                    Err(error) => Err(error.into()),
                };
                (bytes, result)
            }
        }))
        .buffer_unordered(MAX_PARSER_TASKS);
        let mut notified = HashSet::new();
        while let Some((bytes, result)) = results.next().await {
            progress.processed_files = progress.processed_files.saturating_add(1);
            progress.processed_bytes = progress.processed_bytes.saturating_add(bytes);
            match result {
                Ok(outcome) => {
                    record_outcome(&mut report, &outcome);
                    send_session_committed(updates, &mut notified, generation, &outcome.session_id)
                        .await;
                }
                Err(error) if !shutdown.is_cancelled() => {
                    report.failed_files = report.failed_files.saturating_add(1);
                    report.reconcile_again = true;
                    report.failures.push(format!("{error:#}"));
                    progress.failed_files = progress.failed_files.saturating_add(1);
                }
                Err(_) => anyhow::bail!("index reconcile cancelled"),
            }
            if foreground {
                send_update(
                    updates,
                    IndexUpdate::Progress {
                        generation,
                        progress: progress.clone(),
                    },
                )
                .await;
            }
        }
        if shutdown.is_cancelled() {
            anyhow::bail!("index reconcile cancelled");
        }
        if report.indexed_files > 0 {
            let relationship_updates = reconcile_plan_handoffs(&self.database).await?;
            for session_id in &relationship_updates {
                send_update(
                    updates,
                    IndexUpdate::SessionCommitted {
                        generation,
                        session_id: session_id.clone(),
                    },
                )
                .await;
            }
            report.updated_sessions.extend(relationship_updates);
        }
        report.updated_sessions.sort();
        report.updated_sessions.dedup();
        send_update(
            updates,
            IndexUpdate::Completed {
                report: report.clone(),
                foreground,
            },
        )
        .await;
        Ok(report)
    }

    pub async fn run(
        &self,
        watch_events: mpsc::Receiver<WatchEvent>,
        shutdown: CancellationToken,
    ) -> Result<()> {
        self.run_with_updates(watch_events, shutdown, None, false)
            .await
    }

    pub async fn run_with_updates(
        &self,
        mut watch_events: mpsc::Receiver<WatchEvent>,
        shutdown: CancellationToken,
        updates: Option<mpsc::Sender<IndexUpdate>>,
        foreground_first: bool,
    ) -> Result<()> {
        let mut command_receiver = self
            .command_receiver
            .lock()
            .await
            .take()
            .ok_or_else(|| anyhow!("index coordinator is already running"))?;
        let (completion_sender, mut completion_receiver) = mpsc::channel(8);
        let mut runtime = RuntimeScheduler::new(self, updates, shutdown.clone(), completion_sender);
        let generation = self.next_generation();
        if foreground_first {
            send_update(
                runtime.updates.as_ref(),
                IndexUpdate::Discovering { generation },
            )
            .await;
        }
        let now_micros = chrono::Utc::now().timestamp_micros();
        let discovery = self
            .discover(generation, now_micros, shutdown.clone())
            .await?;
        runtime
            .apply_full_discovery(discovery, generation, now_micros, foreground_first)
            .await?;
        let mut bootstrap_pending = foreground_first;
        runtime.start_available()?;
        runtime.maybe_finalize_cycle(&mut bootstrap_pending).await?;

        let mut scheduler_tick = tokio::time::interval(SCHEDULER_TICK);
        scheduler_tick.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
        let mut safety_sweep = tokio::time::interval_at(
            tokio::time::Instant::now() + FULL_SWEEP_INTERVAL,
            FULL_SWEEP_INTERVAL,
        );
        safety_sweep.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
        let mut hot_refresh = tokio::time::interval_at(
            tokio::time::Instant::now() + HOT_REFRESH_INTERVAL,
            HOT_REFRESH_INTERVAL,
        );
        hot_refresh.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
        let mut watch_open = true;
        let mut commands_open = true;
        let mut draining = false;
        let mut next_recovery_sweep = Instant::now();
        let mut recovery_backoff = 0_usize;
        let mut recovery_requested = false;

        loop {
            tokio::select! {
                biased;
                () = shutdown.cancelled(), if !draining => {
                    draining = true;
                }
                completion = completion_receiver.recv() => {
                    if let Some(completion) = completion {
                        runtime.handle_completion(completion).await?;
                    }
                }
                command = command_receiver.recv(), if commands_open && !draining => {
                    match command {
                        Some(CoordinatorCommand::EnsureSession(session_id)) => {
                            runtime.ensure_session(&session_id).await?;
                        }
                        None => commands_open = false,
                    }
                }
                event = watch_events.recv(), if watch_open && !draining => {
                    match event {
                        Some(WatchEvent::Paths(paths)) => {
                            runtime.handle_paths(paths, self.next_generation()).await?;
                        }
                        Some(WatchEvent::Reconcile) => {
                            recovery_requested = true;
                        }
                        Some(WatchEvent::Degraded(message)) => {
                            tracing::warn!(%message, "source watcher requested recovery");
                            recovery_requested = true;
                        }
                        None => watch_open = false,
                    }
                }
                _ = safety_sweep.tick(), if !draining => {
                    recovery_requested = true;
                    recovery_backoff = 0;
                    next_recovery_sweep = Instant::now();
                }
                _ = hot_refresh.tick(), if !draining && runtime.cycle.is_none() => {
                    runtime.audit_hot_sessions(self.next_generation()).await?;
                }
                _ = scheduler_tick.tick() => {}
            }
            if !draining {
                runtime.start_available()?;
                runtime.maybe_finalize_cycle(&mut bootstrap_pending).await?;
                runtime.flush_relationships_if_idle().await?;
                if recovery_requested
                    && runtime.cycle.is_none()
                    && Instant::now() >= next_recovery_sweep
                {
                    let generation = self.next_generation();
                    let now_micros = chrono::Utc::now().timestamp_micros();
                    let discovery = self
                        .discover(generation, now_micros, shutdown.clone())
                        .await?;
                    recovery_requested = !discovery.issues.is_empty();
                    runtime
                        .apply_full_discovery(discovery, generation, now_micros, false)
                        .await?;
                    let delay =
                        FULL_SWEEP_BACKOFF[recovery_backoff.min(FULL_SWEEP_BACKOFF.len() - 1)];
                    recovery_backoff = recovery_backoff.saturating_add(1);
                    next_recovery_sweep = Instant::now() + delay;
                }
            }
            if draining && runtime.inflight.is_empty() {
                return Ok(());
            }
        }
    }

    pub(super) async fn discover(
        &self,
        generation: u64,
        now_micros: i64,
        shutdown: CancellationToken,
    ) -> Result<Discovery> {
        let roots = self.roots.clone();
        let max_event_bytes = self.max_event_bytes;
        let policy = self.policy;
        tokio::task::spawn_blocking(move || {
            discover_sources_cancellable(
                &roots,
                max_event_bytes,
                generation,
                now_micros,
                policy,
                &shutdown,
            )
        })
        .await
        .context("metadata discovery task panicked")?
    }

    #[must_use]
    pub fn generation(&self) -> u64 {
        self.generation.load(Ordering::SeqCst)
    }

    pub(super) fn next_generation(&self) -> u64 {
        self.generation.fetch_add(1, Ordering::SeqCst) + 1
    }
}

impl CoordinatorHandle {
    #[cfg(test)]
    pub(crate) fn poison_catalog_for_test(&self) {
        let shared = Arc::clone(&self.shared);
        let injected = std::thread::spawn(move || {
            let Ok(_guard) = shared.write() else {
                panic!("catalog lock was already poisoned before fault injection");
            };
            panic!("injected index catalog lock poison");
        })
        .join();
        assert!(injected.is_err(), "fault injection must poison the lock");
    }

    pub fn ensure_session(
        &self,
        session_id: &str,
    ) -> std::result::Result<SessionSyncStatus, CoordinatorError> {
        let mut shared = self
            .shared
            .write()
            .map_err(|_| CoordinatorError::LockPoisoned {
                context: "queueing direct session synchronization",
            })?;
        let has_snapshot = shared.snapshots.contains(session_id);
        if let Some(state) = shared.sync_states.get(session_id).copied() {
            return Ok(SessionSyncStatus {
                session_id: session_id.to_owned(),
                state,
                has_snapshot,
            });
        }
        let freshness = shared.freshness.get(session_id).copied();
        let state = match freshness {
            Some(SessionFreshness::SourceMissing) => SessionSyncState::SourceMissing,
            _ if shared.catalog.contains_key(session_id) => SessionSyncState::Queued,
            None => {
                if shared.catalog_ready {
                    SessionSyncState::NotFound
                } else {
                    SessionSyncState::Checking
                }
            }
            Some(SessionFreshness::Current) => SessionSyncState::Current,
            Some(SessionFreshness::Checking | SessionFreshness::Stale) => SessionSyncState::Queued,
        };
        if matches!(state, SessionSyncState::Queued | SessionSyncState::Checking) {
            shared.sync_states.insert(session_id.to_owned(), state);
            if let Err(error) = self
                .commands
                .try_send(CoordinatorCommand::EnsureSession(session_id.to_owned()))
            {
                shared.sync_states.remove(session_id);
                return Err(CoordinatorError::QueueUnavailable {
                    reason: error.to_string(),
                });
            }
        }
        Ok(SessionSyncStatus {
            session_id: session_id.to_owned(),
            state,
            has_snapshot,
        })
    }

    pub fn freshness(
        &self,
        session_id: &str,
    ) -> std::result::Result<SessionFreshness, CoordinatorError> {
        Ok(self
            .shared
            .read()
            .map_err(|_| CoordinatorError::LockPoisoned {
                context: "reading session freshness",
            })?
            .freshness
            .get(session_id)
            .copied()
            .unwrap_or(SessionFreshness::Checking))
    }
}

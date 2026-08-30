use super::*;

impl RuntimeScheduler {
    pub(super) fn new(
        coordinator: &IndexCoordinator,
        updates: Option<mpsc::Sender<IndexUpdate>>,
        shutdown: CancellationToken,
        completion_sender: mpsc::Sender<WorkCompletion>,
    ) -> Self {
        Self {
            database: coordinator.database.clone(),
            writer: coordinator.writer.clone(),
            roots: coordinator.roots.clone(),
            max_event_bytes: coordinator.max_event_bytes,
            gate: coordinator.io_gate.clone(),
            shared: Arc::clone(&coordinator.shared),
            updates,
            shutdown,
            queue: BinaryHeap::new(),
            queued: HashMap::new(),
            inflight: HashMap::new(),
            deferred: HashMap::new(),
            hot_sessions: HashSet::new(),
            lease_counts: HashMap::new(),
            completion_sender,
            last_high_activity: Instant::now(),
            background_hold: None,
            cycle: None,
            relationships_dirty: false,
        }
    }

    pub(super) async fn apply_full_discovery(
        &mut self,
        mut discovery: Discovery,
        generation: u64,
        now_micros: i64,
        foreground: bool,
    ) -> Result<()> {
        let mut progress = IndexProgress {
            total_files: discovery.sources.len() as u64,
            processed_files: 0,
            total_bytes: discovery.total_bytes,
            processed_bytes: 0,
            failed_files: 0,
            excluded_files: 0,
            excluded_bytes: 0,
        };
        if foreground {
            send_update(
                self.updates.as_ref(),
                IndexUpdate::Progress {
                    generation,
                    progress: progress.clone(),
                },
            )
            .await;
        }
        let database = self.database.clone();
        let max_event_bytes = self.max_event_bytes;
        let gate = self.gate.clone();
        let shutdown = self.shutdown.clone();
        let discovered_sources = std::mem::take(&mut discovery.sources);
        let all_discovered_keys = discovered_sources
            .iter()
            .map(source_key)
            .collect::<HashSet<_>>();
        let mut scans = stream::iter(discovered_sources.into_iter().map(|source| {
            let database = database.clone();
            let shutdown = shutdown.clone();
            let lease = gate.register(WorkPriority::Recent);
            let bytes = source.source.size_bytes;
            let fallback = source.clone();
            async move {
                let result = match lease {
                    Ok(lease) => {
                        refresh_catalog_source(
                            database,
                            source,
                            max_event_bytes,
                            now_micros,
                            shutdown,
                            lease,
                        )
                        .await
                    }
                    Err(error) => Err(error.into()),
                };
                (bytes, fallback, result)
            }
        }))
        .buffer_unordered(MAX_PARSER_TASKS);
        let mut catalog_outcomes = Vec::new();
        let mut failed_sources = Vec::new();
        let mut failures = Vec::new();
        while let Some((bytes, source, result)) = scans.next().await {
            progress.processed_files = progress.processed_files.saturating_add(1);
            progress.processed_bytes = progress.processed_bytes.saturating_add(bytes);
            match result {
                Ok(outcome) => {
                    catalog_outcomes.push(outcome);
                }
                Err(error) if !self.shutdown.is_cancelled() => {
                    progress.failed_files = progress.failed_files.saturating_add(1);
                    failures.push(format!("{error:#}"));
                    failed_sources.push(source);
                }
                Err(_) => anyhow::bail!("catalog discovery cancelled"),
            }
            if foreground {
                send_update(
                    self.updates.as_ref(),
                    IndexUpdate::Progress {
                        generation,
                        progress: progress.clone(),
                    },
                )
                .await;
            }
        }
        discovery.sources = catalog_outcomes
            .iter()
            .map(|outcome| outcome.source.clone())
            .collect();
        let stored = load_stored_sources(&self.database).await?;
        let stored_by_key = stored
            .iter()
            .cloned()
            .map(|source| (stored_key(source.root_kind, &source.relative_path), source))
            .collect::<HashMap<_, _>>();
        for mut source in failed_sources {
            let Some(session_id) = stored_by_key
                .get(&source_key(&source))
                .and_then(|stored| stored.session_id.clone())
            else {
                continue;
            };
            source.session_id = session_id;
            discovery.sources.push(source);
        }
        let snapshots = stored
            .iter()
            .filter(|source| source.snapshot_revision > 0)
            .filter_map(|source| source.session_id.clone())
            .collect::<HashSet<_>>();
        let discovered_ids = discovery
            .sources
            .iter()
            .map(|source| source.session_id.clone())
            .collect::<HashSet<_>>();
        let mut missing = Vec::new();
        let mut catalog_updates = catalog_outcomes
            .iter()
            .filter(|outcome| outcome.changed)
            .map(|outcome| outcome.source.session_id.clone())
            .collect::<HashSet<_>>();
        if discovery.issues.is_empty() {
            for source in &stored {
                if !all_discovered_keys
                    .contains(&stored_key(source.root_kind, &source.relative_path))
                    && source.scan_state != "source_missing"
                {
                    missing.push((source.root_kind, source.relative_path.clone()));
                    if let Some(session_id) = &source.session_id {
                        catalog_updates.insert(session_id.clone());
                    }
                }
            }
        }
        let mut present = Vec::new();
        let mut freshness = HashMap::new();
        let mut catalog = HashMap::new();
        for source in &discovery.sources {
            let source = Arc::new(source.clone());
            let stored_source = stored_by_key.get(&source_key(&source));
            let current = stored_source.is_some_and(|stored| snapshot_matches(stored, &source));
            let has_snapshot = snapshots.contains(&source.session_id);
            let session_freshness = if current {
                SessionFreshness::Current
            } else if has_snapshot {
                SessionFreshness::Stale
            } else {
                SessionFreshness::Checking
            };
            freshness.insert(source.session_id.clone(), session_freshness);
            catalog.insert(source.session_id.clone(), Arc::clone(&source));
            if let Some(stored) = stored_source
                && stored.scan_state == "source_missing"
            {
                present.push((stored.root_kind, stored.relative_path.clone()));
                catalog_updates.insert(source.session_id.clone());
            }
        }
        for source in &stored {
            if let Some(session_id) = &source.session_id
                && !discovered_ids.contains(session_id)
                && !all_discovered_keys
                    .contains(&stored_key(source.root_kind, &source.relative_path))
            {
                freshness.insert(session_id.clone(), SessionFreshness::SourceMissing);
            }
        }
        self.writer.mark_sources_missing(missing.clone()).await?;
        self.writer.mark_sources_present(present).await?;
        self.hot_sessions
            .retain(|session_id| discovered_ids.contains(session_id));
        {
            let mut shared = self.write_shared("installing full discovery state")?;
            shared.catalog_ready = true;
            shared.catalog = catalog;
            shared.freshness = freshness;
            shared.snapshots = snapshots;
            shared.sync_states.retain(|session_id, _| {
                self.queued.contains_key(session_id) || self.inflight.contains_key(session_id)
            });
        }
        let mut catalog_updates = catalog_updates.into_iter().collect::<Vec<_>>();
        catalog_updates.sort();
        for session_id in catalog_updates {
            send_update(
                self.updates.as_ref(),
                IndexUpdate::CatalogCommitted {
                    generation,
                    session_id,
                },
            )
            .await;
        }
        let report = ReconcileReport {
            generation,
            discovered_files: progress.total_files,
            discovered_bytes: discovery.total_bytes,
            indexed_files: catalog_outcomes
                .iter()
                .filter(|outcome| outcome.changed)
                .count() as u64,
            failed_files: progress.failed_files,
            removed_files: missing.len() as u64,
            discovery_issues: discovery.issues.len() as u64,
            reconcile_again: !discovery.issues.is_empty() || !failures.is_empty(),
            failures,
            ..ReconcileReport::default()
        };
        self.cycle = Some(ActiveCycle {
            report,
            progress,
            pending_sessions: HashSet::new(),
            foreground,
        });
        Ok(())
    }

    pub(super) fn enqueue(&mut self, mut work: WorkItem) -> Result<()> {
        let session_id = work.source.session_id.clone();
        if work.priority >= WorkPriority::Recent {
            self.last_high_activity = Instant::now();
            if self.background_hold.is_none() {
                self.background_hold = Some(self.gate.register(WorkPriority::Recent)?);
            }
        }
        if let Some(inflight) = self.inflight.get_mut(&session_id) {
            inflight.lease.promote(work.priority)?;
            if inflight.work.fingerprint == work.fingerprint {
                inflight.work.priority = inflight.work.priority.max(work.priority);
                inflight.work.cycle_generation =
                    inflight.work.cycle_generation.or(work.cycle_generation);
                return Ok(());
            }
            if let Some(existing) = self.deferred.get(&session_id) {
                work.priority = work.priority.max(existing.priority);
                work.cycle_generation = work.cycle_generation.or(existing.cycle_generation);
            }
            self.deferred.insert(session_id.clone(), work);
            self.set_sync_state(&session_id, SessionSyncState::Queued)?;
            return Ok(());
        }
        if let Some(existing) = self.queued.get(&session_id) {
            work.priority = work.priority.max(existing.priority);
            work.cycle_generation = work.cycle_generation.or(existing.cycle_generation);
            if work.fingerprint == existing.fingerprint && work.priority == existing.priority {
                return Ok(());
            }
        }
        self.queued.insert(session_id.clone(), work.clone());
        self.queue.push(work);
        self.set_sync_state(&session_id, SessionSyncState::Queued)?;
        Ok(())
    }

    pub(super) fn start_available(&mut self) -> Result<()> {
        if self.last_high_activity.elapsed() >= BACKGROUND_IDLE_DELAY {
            self.background_hold = None;
        }
        loop {
            let high_count = self
                .inflight
                .values()
                .filter(|inflight| inflight.work.priority >= WorkPriority::Recent)
                .count();
            let interactive_active = self
                .inflight
                .values()
                .any(|inflight| inflight.work.priority == WorkPriority::Interactive);
            let Some(next) = self.peek_valid() else { break };
            if next.priority >= WorkPriority::Recent {
                let can_start = high_count < MAX_PARSER_TASKS
                    || (next.priority == WorkPriority::Interactive && !interactive_active);
                if !can_start {
                    break;
                }
                let work = self.pop_valid().expect("peeked valid work");
                self.start(work)?;
                continue;
            }
            let background_active = self
                .inflight
                .values()
                .any(|inflight| inflight.work.priority == WorkPriority::Background);
            if high_count == 0
                && !background_active
                && self.last_high_activity.elapsed() >= BACKGROUND_IDLE_DELAY
            {
                let work = self.pop_valid().expect("peeked valid work");
                self.start(work)?;
            }
            break;
        }
        Ok(())
    }

    pub(super) fn peek_valid(&mut self) -> Option<&WorkItem> {
        loop {
            let candidate = self.queue.peek()?;
            let valid = self
                .queued
                .get(&candidate.source.session_id)
                .is_some_and(|current| {
                    current.fingerprint == candidate.fingerprint
                        && current.priority == candidate.priority
                        && current.cycle_generation == candidate.cycle_generation
                });
            if valid {
                return self.queue.peek();
            }
            self.queue.pop();
        }
    }

    pub(super) fn pop_valid(&mut self) -> Option<WorkItem> {
        self.peek_valid()?;
        let work = self.queue.pop()?;
        self.queued.remove(&work.source.session_id);
        Some(work)
    }

    pub(super) fn start(&mut self, work: WorkItem) -> Result<()> {
        let session_id = work.source.session_id.clone();
        let lease = self.gate.register(work.priority)?;
        self.inflight.insert(
            session_id.clone(),
            InflightWork {
                work: work.clone(),
                lease: lease.clone(),
                cancel: self.shutdown.child_token(),
            },
        );
        self.set_sync_state(&session_id, SessionSyncState::Indexing)?;
        let database = self.database.clone();
        let writer = self.writer.clone();
        let shutdown = self
            .inflight
            .get(&session_id)
            .expect("inserted inflight work")
            .cancel
            .clone();
        let completion_sender = self.completion_sender.clone();
        let max_event_bytes = self.max_event_bytes;
        tokio::spawn(async move {
            let result = scan_source_with_lease(
                database,
                writer,
                work.source.as_ref().clone(),
                max_event_bytes,
                chrono::Utc::now().timestamp_micros(),
                shutdown,
                lease,
            )
            .await;
            let _ = completion_sender
                .send(WorkCompletion { work, result })
                .await;
        });
        Ok(())
    }
}

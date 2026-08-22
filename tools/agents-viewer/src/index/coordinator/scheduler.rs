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
            policy: coordinator.policy,
            gate: coordinator.io_gate.clone(),
            shared: Arc::clone(&coordinator.shared),
            updates,
            shutdown,
            queue: BinaryHeap::new(),
            queued: HashMap::new(),
            inflight: HashMap::new(),
            deferred: HashMap::new(),
            hot_sessions: HashSet::new(),
            completion_sender,
            last_high_activity: Instant::now(),
            background_hold: None,
            cycle: None,
            relationships_dirty: false,
        }
    }

    pub(super) async fn apply_full_discovery(
        &mut self,
        discovery: Discovery,
        generation: u64,
        now_micros: i64,
        foreground: bool,
    ) -> Result<()> {
        let stored = load_stored_sources(&self.database).await?;
        let stored_by_key = stored
            .iter()
            .cloned()
            .map(|source| (stored_key(source.root_kind, &source.relative_path), source))
            .collect::<HashMap<_, _>>();
        let snapshots = stored
            .iter()
            .filter_map(|source| source.session_id.clone())
            .collect::<HashSet<_>>();
        let discovered_keys = discovery
            .sources
            .iter()
            .map(source_key)
            .collect::<HashSet<_>>();
        let discovered_ids = discovery
            .sources
            .iter()
            .map(|source| source.session_id.clone())
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
        let mut freshness = HashMap::new();
        let mut catalog = HashMap::new();
        let mut pending_sessions = HashSet::new();
        let mut progress = IndexProgress {
            total_files: 0,
            processed_files: 0,
            total_bytes: 0,
            processed_bytes: 0,
            failed_files: 0,
            excluded_files: 0,
            excluded_bytes: 0,
        };
        for source in &discovery.sources {
            let source = Arc::new(source.clone());
            let stored_source = stored_by_key.get(&source_key(&source));
            let current = stored_source.is_some_and(|stored| metadata_matches(stored, &source));
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
            if current && stored_source.is_some_and(|stored| stored.scan_state == "source_missing")
            {
                let stored = stored_source.expect("checked above");
                present.push((stored.root_kind, stored.relative_path.clone()));
            }
            match automatic_priority(self.policy, &source, now_micros) {
                Some(WorkPriority::Recent) => {
                    self.hot_sessions.insert(source.session_id.clone());
                    progress.total_files = progress.total_files.saturating_add(1);
                    progress.total_bytes = progress
                        .total_bytes
                        .saturating_add(source.source.size_bytes);
                    if current {
                        progress.processed_files = progress.processed_files.saturating_add(1);
                        progress.processed_bytes = progress
                            .processed_bytes
                            .saturating_add(source.source.size_bytes);
                    } else {
                        pending_sessions.insert(source.session_id.clone());
                        self.enqueue(WorkItem::new(
                            Arc::clone(&source),
                            WorkPriority::Recent,
                            Some(generation),
                        ));
                    }
                }
                Some(WorkPriority::Background) => {
                    progress.excluded_files = progress.excluded_files.saturating_add(1);
                    progress.excluded_bytes = progress
                        .excluded_bytes
                        .saturating_add(source.source.size_bytes);
                    if !current {
                        self.enqueue(WorkItem::new(
                            Arc::clone(&source),
                            WorkPriority::Background,
                            None,
                        ));
                    }
                }
                Some(WorkPriority::Interactive) => unreachable!(),
                None => {
                    progress.excluded_files = progress.excluded_files.saturating_add(1);
                    progress.excluded_bytes = progress
                        .excluded_bytes
                        .saturating_add(source.source.size_bytes);
                }
            }
        }
        for source in &stored {
            if let Some(session_id) = &source.session_id
                && !discovered_ids.contains(session_id)
                && !discovered_keys.contains(&stored_key(source.root_kind, &source.relative_path))
            {
                freshness.insert(session_id.clone(), SessionFreshness::SourceMissing);
            }
        }
        self.writer.mark_sources_missing(missing.clone()).await?;
        self.writer.mark_sources_present(present).await?;
        self.hot_sessions
            .retain(|session_id| discovered_ids.contains(session_id));
        {
            let mut shared = self.shared.write().expect("index catalog lock poisoned");
            shared.catalog_ready = true;
            shared.catalog = catalog;
            shared.freshness = freshness;
            shared.snapshots = snapshots;
            shared.sync_states.retain(|session_id, _| {
                self.queued.contains_key(session_id) || self.inflight.contains_key(session_id)
            });
        }
        let report = ReconcileReport {
            generation,
            discovered_files: discovery.sources.len() as u64,
            discovered_bytes: discovery.total_bytes,
            removed_files: missing.len() as u64,
            discovery_issues: discovery.issues.len() as u64,
            excluded_files: progress.excluded_files,
            excluded_bytes: progress.excluded_bytes,
            reconcile_again: !discovery.issues.is_empty(),
            ..ReconcileReport::default()
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
        self.cycle = Some(ActiveCycle {
            report,
            progress,
            pending_sessions,
            foreground,
        });
        Ok(())
    }

    pub(super) fn enqueue(&mut self, mut work: WorkItem) {
        let session_id = work.source.session_id.clone();
        if work.priority >= WorkPriority::Recent {
            self.last_high_activity = Instant::now();
            if self.background_hold.is_none() {
                self.background_hold = Some(self.gate.register(WorkPriority::Recent));
            }
        }
        if let Some(inflight) = self.inflight.get_mut(&session_id) {
            inflight.lease.promote(work.priority);
            if inflight.work.fingerprint == work.fingerprint {
                inflight.work.priority = inflight.work.priority.max(work.priority);
                inflight.work.cycle_generation =
                    inflight.work.cycle_generation.or(work.cycle_generation);
                return;
            }
            if let Some(existing) = self.deferred.get(&session_id) {
                work.priority = work.priority.max(existing.priority);
                work.cycle_generation = work.cycle_generation.or(existing.cycle_generation);
            }
            self.deferred.insert(session_id.clone(), work);
            self.set_sync_state(&session_id, SessionSyncState::Queued);
            return;
        }
        if let Some(existing) = self.queued.get(&session_id) {
            work.priority = work.priority.max(existing.priority);
            work.cycle_generation = work.cycle_generation.or(existing.cycle_generation);
            if work.fingerprint == existing.fingerprint && work.priority == existing.priority {
                return;
            }
        }
        self.queued.insert(session_id.clone(), work.clone());
        self.queue.push(work);
        self.set_sync_state(&session_id, SessionSyncState::Queued);
    }

    pub(super) fn start_available(&mut self) {
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
                self.start(work);
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
                self.start(work);
            }
            break;
        }
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

    pub(super) fn start(&mut self, work: WorkItem) {
        let session_id = work.source.session_id.clone();
        let lease = self.gate.register(work.priority);
        self.inflight.insert(
            session_id.clone(),
            InflightWork {
                work: work.clone(),
                lease: lease.clone(),
            },
        );
        self.set_sync_state(&session_id, SessionSyncState::Indexing);
        let database = self.database.clone();
        let writer = self.writer.clone();
        let shutdown = self.shutdown.clone();
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
    }
}

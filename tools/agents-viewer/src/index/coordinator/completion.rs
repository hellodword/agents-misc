use super::*;

impl RuntimeScheduler {
    pub(super) async fn handle_completion(&mut self, completion: WorkCompletion) -> Result<()> {
        let session_id = completion.work.source.session_id.clone();
        let actual = self
            .inflight
            .remove(&session_id)
            .map_or(completion.work, |inflight| inflight.work);
        if actual.priority >= WorkPriority::Recent {
            self.last_high_activity = Instant::now();
            if self.background_hold.is_none() {
                self.background_hold = Some(self.gate.register(WorkPriority::Recent));
            }
        }
        match completion.result {
            Ok(outcome) => {
                {
                    let mut shared = self.shared.write().expect("index catalog lock poisoned");
                    shared
                        .freshness
                        .insert(outcome.session_id.clone(), SessionFreshness::Current);
                    shared.snapshots.insert(outcome.session_id.clone());
                    shared.sync_states.remove(&outcome.session_id);
                }
                self.relationships_dirty = true;
                send_update(
                    self.updates.as_ref(),
                    IndexUpdate::SessionCommitted {
                        generation: self
                            .cycle
                            .as_ref()
                            .map_or(0, |cycle| cycle.report.generation),
                        session_id: outcome.session_id.clone(),
                    },
                )
                .await;
                if let Some(cycle) = self.cycle.as_mut()
                    && cycle.pending_sessions.remove(&session_id)
                {
                    cycle.progress.processed_files =
                        cycle.progress.processed_files.saturating_add(1);
                    cycle.progress.processed_bytes = cycle
                        .progress
                        .processed_bytes
                        .saturating_add(actual.source.source.size_bytes);
                    record_outcome(&mut cycle.report, &outcome);
                    if cycle.foreground {
                        send_update(
                            self.updates.as_ref(),
                            IndexUpdate::Progress {
                                generation: cycle.report.generation,
                                progress: cycle.progress.clone(),
                            },
                        )
                        .await;
                    }
                }
                if outcome.changed_during_scan {
                    self.rediscover_and_enqueue(&actual.source.path, WorkPriority::Recent)
                        .await?;
                }
            }
            Err(error) if !self.shutdown.is_cancelled() => {
                let has_snapshot = self
                    .shared
                    .read()
                    .expect("index catalog lock poisoned")
                    .snapshots
                    .contains(&session_id);
                {
                    let mut shared = self.shared.write().expect("index catalog lock poisoned");
                    shared.freshness.insert(
                        session_id.clone(),
                        if has_snapshot {
                            SessionFreshness::Stale
                        } else {
                            SessionFreshness::Checking
                        },
                    );
                    shared.sync_states.remove(&session_id);
                }
                if let Some(cycle) = self.cycle.as_mut()
                    && cycle.pending_sessions.remove(&session_id)
                {
                    cycle.progress.processed_files =
                        cycle.progress.processed_files.saturating_add(1);
                    cycle.progress.processed_bytes = cycle
                        .progress
                        .processed_bytes
                        .saturating_add(actual.source.source.size_bytes);
                    cycle.progress.failed_files = cycle.progress.failed_files.saturating_add(1);
                    cycle.report.failed_files = cycle.report.failed_files.saturating_add(1);
                    cycle.report.reconcile_again = true;
                    cycle.report.failures.push(format!("{error:#}"));
                }
            }
            Err(_) => {}
        }
        if let Some(deferred) = self.deferred.remove(&session_id) {
            self.enqueue(deferred);
        }
        Ok(())
    }

    pub(super) async fn ensure_session(&mut self, session_id: &str) -> Result<()> {
        let source = self
            .shared
            .read()
            .expect("index catalog lock poisoned")
            .catalog
            .get(session_id)
            .cloned();
        if let Some(source) = source {
            self.hot_sessions.insert(session_id.to_owned());
            let generation = self
                .cycle
                .as_ref()
                .map_or(source.source.generation, |cycle| cycle.report.generation);
            self.handle_paths_with_priority(
                vec![source.path.clone()],
                generation,
                WorkPriority::Interactive,
            )
            .await?;
            return Ok(());
        }
        let state = if self
            .shared
            .read()
            .expect("index catalog lock poisoned")
            .freshness
            .get(session_id)
            == Some(&SessionFreshness::SourceMissing)
        {
            SessionSyncState::SourceMissing
        } else {
            SessionSyncState::NotFound
        };
        self.clear_sync_state(session_id);
        self.publish_session_state(session_id, state).await;
        Ok(())
    }

    pub(super) async fn handle_paths(
        &mut self,
        paths: Vec<PathBuf>,
        generation: u64,
    ) -> Result<()> {
        self.handle_paths_with_priority(paths, generation, WorkPriority::Recent)
            .await
    }

    pub(super) async fn handle_paths_with_priority(
        &mut self,
        paths: Vec<PathBuf>,
        generation: u64,
        priority: WorkPriority,
    ) -> Result<()> {
        let mut unique = paths
            .into_iter()
            .collect::<HashSet<_>>()
            .into_iter()
            .collect::<Vec<_>>();
        unique.sort();
        let mut rediscovered = Vec::new();
        let mut deleted = Vec::new();
        for path in unique {
            if path.is_dir() {
                let roots = self.roots.clone();
                let directory = path.clone();
                let sources = tokio::task::spawn_blocking(move || {
                    walkdir::WalkDir::new(directory)
                        .follow_links(false)
                        .into_iter()
                        .filter_map(Result::ok)
                        .filter(|entry| entry.file_type().is_file())
                        .filter_map(|entry| {
                            discover_source_path(&roots, entry.path(), generation)
                                .ok()
                                .flatten()
                        })
                        .collect::<Vec<_>>()
                })
                .await
                .context("targeted directory discovery task panicked")?;
                rediscovered.extend(sources);
                continue;
            }
            if path.extension().and_then(|value| value.to_str()) != Some("jsonl") {
                if !path.exists() {
                    deleted.push(path);
                }
                continue;
            }
            if path.is_file() {
                let roots = self.roots.clone();
                let source_path = path.clone();
                match tokio::task::spawn_blocking(move || {
                    discover_source_path(&roots, &source_path, generation)
                })
                .await
                .context("targeted source discovery task panicked")?
                {
                    Ok(Some(source)) => rediscovered.push(source),
                    Ok(None) => {}
                    Err(_) => {
                        // A racing append or rename should produce another event. Hot paths are
                        // also retried by the targeted audit before the full safety sweep.
                    }
                }
            } else {
                deleted.push(path);
            }
        }
        let rediscovered_ids = rediscovered
            .iter()
            .map(|source| source.session_id.clone())
            .collect::<HashSet<_>>();
        let mut resolved_current = Vec::new();
        for source in rediscovered {
            let source = Arc::new(source);
            let existing = self
                .shared
                .read()
                .expect("index catalog lock poisoned")
                .catalog
                .get(&source.session_id)
                .cloned();
            if existing
                .as_ref()
                .is_some_and(|current| !source_precedes(&source, current))
                && existing
                    .as_ref()
                    .is_some_and(|current| current.path != source.path)
            {
                continue;
            }
            let stored = load_stored_source(&self.database, &source).await?;
            let current = stored
                .as_ref()
                .is_some_and(|stored| metadata_matches(stored, &source));
            let has_snapshot = self
                .shared
                .read()
                .expect("index catalog lock poisoned")
                .snapshots
                .contains(&source.session_id);
            self.hot_sessions.insert(source.session_id.clone());
            {
                let mut shared = self.shared.write().expect("index catalog lock poisoned");
                shared
                    .catalog
                    .insert(source.session_id.clone(), Arc::clone(&source));
                shared.freshness.insert(
                    source.session_id.clone(),
                    if current {
                        SessionFreshness::Current
                    } else if has_snapshot {
                        SessionFreshness::Stale
                    } else {
                        SessionFreshness::Checking
                    },
                );
            }
            if current {
                if let Some(stored) = stored
                    && stored.scan_state == "source_missing"
                {
                    self.writer
                        .mark_source_present(stored.root_kind, stored.relative_path)
                        .await?;
                }
                if self
                    .shared
                    .write()
                    .expect("index catalog lock poisoned")
                    .sync_states
                    .remove(&source.session_id)
                    .is_some()
                {
                    resolved_current.push(source.session_id.clone());
                }
            } else {
                self.enqueue(WorkItem::new(source, priority, None));
            }
        }
        let mut mark_missing = Vec::new();
        let mut resolved_missing = Vec::new();
        for path in deleted {
            if path.extension().and_then(|value| value.to_str()) == Some("jsonl")
                && let Some(source) = source_coordinates_for_path(&self.roots, &path)
            {
                mark_missing.push(source);
            }
            let matches = self
                .shared
                .read()
                .expect("index catalog lock poisoned")
                .catalog
                .iter()
                .filter(|(_, source)| source.path == path || source.path.starts_with(&path))
                .map(|(session_id, source)| (session_id.clone(), source.clone()))
                .collect::<Vec<_>>();
            for (session_id, source) in matches {
                if rediscovered_ids.contains(&session_id) {
                    continue;
                }
                {
                    let mut shared = self.shared.write().expect("index catalog lock poisoned");
                    shared.catalog.remove(&session_id);
                    if shared.snapshots.contains(&session_id) {
                        shared
                            .freshness
                            .insert(session_id.clone(), SessionFreshness::SourceMissing);
                    }
                    if shared.sync_states.remove(&session_id).is_some() {
                        resolved_missing.push(session_id.clone());
                    }
                }
                self.hot_sessions.remove(&session_id);
                mark_missing.push((source.source.root_kind, source.source.relative_path.clone()));
            }
        }
        mark_missing.sort_by(|left, right| {
            root_kind_value(left.0)
                .cmp(root_kind_value(right.0))
                .then_with(|| left.1.cmp(&right.1))
        });
        mark_missing.dedup();
        self.writer.mark_sources_missing(mark_missing).await?;
        for session_id in resolved_current {
            self.publish_session_state(&session_id, SessionSyncState::Current)
                .await;
        }
        for session_id in resolved_missing {
            self.publish_session_state(&session_id, SessionSyncState::SourceMissing)
                .await;
        }
        self.start_available();
        Ok(())
    }

    pub(super) async fn audit_hot_sessions(&mut self, generation: u64) -> Result<()> {
        let mut paths = {
            let shared = self.shared.read().expect("index catalog lock poisoned");
            self.hot_sessions
                .iter()
                .filter_map(|session_id| shared.catalog.get(session_id))
                .map(|source| source.path.clone())
                .collect::<Vec<_>>()
        };
        paths.sort();
        paths.dedup();
        if paths.is_empty() {
            return Ok(());
        }
        self.handle_paths_with_priority(paths, generation, WorkPriority::Recent)
            .await
    }

    pub(super) async fn rediscover_and_enqueue(
        &mut self,
        path: &Path,
        priority: WorkPriority,
    ) -> Result<()> {
        let roots = self.roots.clone();
        let path = path.to_path_buf();
        let generation = self
            .cycle
            .as_ref()
            .map_or(0, |cycle| cycle.report.generation);
        if let Some(source) =
            tokio::task::spawn_blocking(move || discover_source_path(&roots, &path, generation))
                .await
                .context("targeted source rediscovery task panicked")??
        {
            self.hot_sessions.insert(source.session_id.clone());
            self.enqueue(WorkItem::new(Arc::new(source), priority, None));
        }
        Ok(())
    }

    pub(super) async fn maybe_finalize_cycle(
        &mut self,
        bootstrap_pending: &mut bool,
    ) -> Result<()> {
        if self
            .cycle
            .as_ref()
            .is_none_or(|cycle| !cycle.pending_sessions.is_empty())
        {
            return Ok(());
        }
        if self.relationships_dirty {
            self.flush_relationships().await?;
        }
        let mut cycle = self.cycle.take().expect("checked above");
        cycle.report.updated_sessions.sort();
        cycle.report.updated_sessions.dedup();
        if *bootstrap_pending && report_is_healthy(&cycle.report) {
            self.database.mark_bootstrap_complete().await?;
            *bootstrap_pending = false;
        }
        send_update(
            self.updates.as_ref(),
            IndexUpdate::Completed {
                report: cycle.report,
                foreground: cycle.foreground,
            },
        )
        .await;
        Ok(())
    }

    pub(super) async fn flush_relationships_if_idle(&mut self) -> Result<()> {
        if self.relationships_dirty
            && self.queue.is_empty()
            && self.inflight.is_empty()
            && self.deferred.is_empty()
        {
            self.flush_relationships().await?;
        }
        Ok(())
    }

    pub(super) async fn flush_relationships(&mut self) -> Result<()> {
        let updates = reconcile_plan_handoffs(&self.database).await?;
        let generation = self
            .cycle
            .as_ref()
            .map_or(0, |cycle| cycle.report.generation);
        for session_id in updates {
            if let Some(cycle) = self.cycle.as_mut() {
                cycle.report.updated_sessions.push(session_id.clone());
            }
            send_update(
                self.updates.as_ref(),
                IndexUpdate::SessionCommitted {
                    generation,
                    session_id,
                },
            )
            .await;
        }
        self.relationships_dirty = false;
        Ok(())
    }

    pub(super) fn set_sync_state(&self, session_id: &str, state: SessionSyncState) {
        self.shared
            .write()
            .expect("index catalog lock poisoned")
            .sync_states
            .insert(session_id.to_owned(), state);
    }

    pub(super) fn clear_sync_state(&self, session_id: &str) {
        self.shared
            .write()
            .expect("index catalog lock poisoned")
            .sync_states
            .remove(session_id);
    }

    pub(super) async fn publish_session_state(&self, session_id: &str, state: SessionSyncState) {
        send_update(
            self.updates.as_ref(),
            IndexUpdate::SessionState {
                generation: self
                    .cycle
                    .as_ref()
                    .map_or(0, |cycle| cycle.report.generation),
                session_id: session_id.to_owned(),
                state,
            },
        )
        .await;
    }
}

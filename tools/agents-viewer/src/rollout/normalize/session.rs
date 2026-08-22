use super::*;

pub(super) struct SessionBuilder {
    pub(super) id: String,
    pub(super) source: SourceKind,
    pub(super) parent_thread_id: Option<String>,
    pub(super) parent_relation: Option<SessionParentRelation>,
    pub(super) proposed_plan_hash: Option<String>,
    pub(super) proposed_plan_at_micros: Option<i64>,
    pub(super) handoff_plan_hash: Option<String>,
    pub(super) handoff_at_micros: Option<i64>,
    pub(super) cwd: Option<String>,
    pub(super) title: Option<String>,
    pub(super) preview: Option<String>,
    pub(super) created_at_micros: i64,
    pub(super) updated_at_micros: i64,
    pub(super) cli_version: Option<String>,
    pub(super) provider: Option<String>,
    pub(super) history_line: Option<u64>,
    pub(super) git_branch: Option<String>,
    pub(super) git_commit: Option<String>,
    pub(super) entry_count: u64,
    pub(super) diagnostic_count: u64,
    pub(super) saw_user: bool,
    pub(super) subagent_history_start_ordinal: Option<u64>,
    pub(super) last_inter_agent_source_id: Option<String>,
}

impl SessionBuilder {
    pub(super) fn new(context: &ParseContext, id: String) -> Self {
        let created =
            timestamp_from_filename(&context.file_name).unwrap_or(context.modified_at_micros);
        Self {
            id,
            source: SourceKind::Unknown,
            parent_thread_id: None,
            parent_relation: None,
            proposed_plan_hash: None,
            proposed_plan_at_micros: None,
            handoff_plan_hash: None,
            handoff_at_micros: None,
            cwd: None,
            title: None,
            preview: None,
            created_at_micros: created,
            updated_at_micros: context.modified_at_micros,
            cli_version: None,
            provider: None,
            history_line: None,
            git_branch: None,
            git_commit: None,
            entry_count: 0,
            diagnostic_count: 0,
            saw_user: false,
            subagent_history_start_ordinal: None,
            last_inter_agent_source_id: None,
        }
    }

    pub(super) fn from_record(record: SessionRecord) -> Self {
        let saw_user = !record.title.starts_with("Untitled · ");
        Self {
            id: record.id,
            source: record.source,
            parent_thread_id: record.parent_thread_id,
            parent_relation: record.parent_relation,
            proposed_plan_hash: record.proposed_plan_hash,
            proposed_plan_at_micros: record.proposed_plan_at_micros,
            handoff_plan_hash: record.handoff_plan_hash,
            handoff_at_micros: record.handoff_at_micros,
            cwd: record.cwd,
            title: saw_user.then_some(record.title),
            preview: saw_user.then_some(record.preview),
            created_at_micros: record.created_at_micros,
            updated_at_micros: record.updated_at_micros,
            cli_version: record.cli_version,
            provider: record.provider,
            history_line: record.history_line,
            git_branch: record.git_branch,
            git_commit: record.git_commit,
            entry_count: record.entry_count,
            diagnostic_count: record.diagnostic_count,
            saw_user,
            subagent_history_start_ordinal: None,
            last_inter_agent_source_id: None,
        }
    }

    pub(super) fn apply_metadata(&mut self, payload: &Value, timestamp: Option<i64>, line_no: u64) {
        if let Some(id) = payload_session_id(payload) {
            self.id = id;
        }
        self.cwd = string_option(payload, "cwd").or_else(|| self.cwd.take());
        if let Some(parent) = string_option(payload, "parent_thread_id")
            .or_else(|| source_parent(payload.get("source")))
        {
            self.parent_thread_id = Some(parent);
            self.parent_relation = Some(SessionParentRelation::Parent);
        } else if self.parent_relation != Some(SessionParentRelation::Parent)
            && let Some(parent) = string_option(payload, "forked_from_id")
        {
            self.parent_thread_id = Some(parent);
            self.parent_relation = Some(SessionParentRelation::Fork);
        }
        self.cli_version =
            string_option(payload, "cli_version").or_else(|| self.cli_version.take());
        self.provider = string_option(payload, "model_provider").or_else(|| self.provider.take());
        self.subagent_history_start_ordinal = payload
            .get("subagent_history_start_ordinal")
            .and_then(Value::as_u64)
            .or(self.subagent_history_start_ordinal);
        self.source = source_kind(payload);
        self.history_line = payload
            .get("history_line")
            .and_then(Value::as_u64)
            .or(self.history_line)
            .or(Some(line_no));
        if let Some(metadata_timestamp) = payload
            .get("timestamp")
            .and_then(Value::as_str)
            .and_then(parse_timestamp)
            .or(timestamp)
        {
            self.created_at_micros = metadata_timestamp;
            self.updated_at_micros = self.updated_at_micros.max(metadata_timestamp);
        }
        if let Some(git) = payload.get("git") {
            self.git_branch = string_option(git, "branch");
            self.git_commit =
                string_option(git, "commit_hash").or_else(|| string_option(git, "commit"));
        }
    }

    pub(super) fn is_inherited_ordinal(&self, ordinal: Option<u64>) -> bool {
        ordinal.is_some_and(|ordinal| {
            self.subagent_history_start_ordinal
                .is_some_and(|start| ordinal < start)
        })
    }

    pub(super) fn observe_entry(&mut self, entry: &NormalizedEntry) {
        if entry.kind == EntryKind::Plan
            && let Some(hash) = normalized_plan_hash(&entry.primary_text)
        {
            self.proposed_plan_hash = Some(hash);
            self.proposed_plan_at_micros =
                Some(entry.timestamp_micros.unwrap_or(self.updated_at_micros));
        }
        if !self.saw_user
            && entry.presentation == EntryPresentation::User
            && !entry.primary_text.trim().is_empty()
        {
            if let Some(hash) = handoff_plan_hash(&entry.primary_text) {
                self.handoff_plan_hash = Some(hash);
                self.handoff_at_micros =
                    Some(entry.timestamp_micros.unwrap_or(self.updated_at_micros));
            }
            self.saw_user = true;
            self.title = Some(title_from_user_message(&entry.primary_text));
            self.preview = Some(truncate_graphemes(&entry.primary_text, 160));
        }
    }

    pub(super) fn finish(
        self,
        context: &ParseContext,
        recognized: u64,
        partial: bool,
        incomplete_tail: bool,
    ) -> SessionRecord {
        let completeness = if recognized == 0 {
            Completeness::Unsupported
        } else if partial {
            Completeness::Partial
        } else if context.root_kind == RootKind::Active
            && (incomplete_tail
                || context.now_micros.saturating_sub(self.updated_at_micros) <= 60_000_000)
        {
            Completeness::Live
        } else {
            Completeness::Complete
        };
        let fallback_title = format!(
            "Untitled · {}",
            DateTime::<Utc>::from_timestamp_micros(self.created_at_micros)
                .map_or_else(|| "unknown time".into(), |value| value.to_rfc3339())
        );
        SessionRecord {
            id: self.id,
            source: self.source,
            parent_thread_id: self.parent_thread_id,
            parent_relation: self.parent_relation,
            proposed_plan_hash: self.proposed_plan_hash,
            proposed_plan_at_micros: self.proposed_plan_at_micros,
            handoff_plan_hash: self.handoff_plan_hash,
            handoff_at_micros: self.handoff_at_micros,
            cwd: self.cwd,
            title: self.title.unwrap_or(fallback_title),
            preview: self.preview.unwrap_or_default(),
            created_at_micros: self.created_at_micros,
            updated_at_micros: self.updated_at_micros,
            archived: context.root_kind == RootKind::Archived,
            cli_version: self.cli_version,
            provider: self.provider,
            history_line: self.history_line,
            git_branch: self.git_branch,
            git_commit: self.git_commit,
            entry_count: self.entry_count,
            index_state: if partial {
                IndexState::Partial
            } else {
                IndexState::Ready
            },
            completeness,
            diagnostic_count: self.diagnostic_count,
        }
    }
}

const PLAN_HANDOFF_PREFIX: &str = "A previous agent produced the plan below to accomplish the user's task. Implement the plan in a fresh context. Treat the plan as the source of user intent, re-read files as needed, and carry the work through implementation and verification.";

pub(super) fn handoff_plan_hash(text: &str) -> Option<String> {
    let normalized = normalize_plan_text(text);
    let plan = normalized
        .trim()
        .strip_prefix(PLAN_HANDOFF_PREFIX)?
        .strip_prefix("\n\n")?;
    normalized_plan_hash(plan)
}

pub(super) fn normalized_plan_hash(text: &str) -> Option<String> {
    let normalized = normalize_plan_text(text);
    let plan = normalized.trim();
    (!plan.is_empty()).then(|| sha256(plan.as_bytes()))
}

pub(super) fn normalize_plan_text(text: &str) -> String {
    text.replace("\r\n", "\n").replace('\r', "\n")
}

pub(super) fn title_from_user_message(text: &str) -> String {
    let first_line = text
        .lines()
        .map(str::trim)
        .find(|line| !line.is_empty())
        .unwrap_or_default();
    let without_markdown = first_line
        .trim_start_matches(['#', '-', '*', '+'])
        .trim_start();
    let normalized = without_markdown
        .split_whitespace()
        .collect::<Vec<_>>()
        .join(" ");
    truncate_graphemes(&normalized, 80)
}

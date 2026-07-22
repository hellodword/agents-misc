use std::collections::{HashMap, VecDeque};

use sha2::{Digest, Sha256};

use super::types::{EntryOrigin, NormalizedEntry, ParseSeed};

const DEDUP_LINE_WINDOW: u64 = 10;
const DEDUP_TIME_WINDOW_MICROS: i64 = 2_000_000;

#[derive(Clone)]
struct TrackedEntry {
    line_no: u64,
    entry: NormalizedEntry,
}

pub struct Deduper {
    session_id: String,
    next_sequence: i64,
    occurrences: HashMap<String, u64>,
    recent: VecDeque<TrackedEntry>,
    active_tools: HashMap<String, TrackedEntry>,
}

impl Deduper {
    pub fn new(session_id: String) -> Self {
        Self {
            session_id,
            next_sequence: 0,
            occurrences: HashMap::new(),
            recent: VecDeque::with_capacity(16),
            active_tools: HashMap::new(),
        }
    }

    pub fn from_seed(seed: &ParseSeed) -> Self {
        let mut deduper = Self {
            session_id: seed.session.id.clone(),
            next_sequence: seed.next_sequence,
            occurrences: seed.occurrences.clone(),
            recent: VecDeque::with_capacity(16),
            active_tools: HashMap::new(),
        };
        for (line_no, entry) in &seed.recent {
            let tracked = TrackedEntry {
                line_no: *line_no,
                entry: entry.clone(),
            };
            deduper.track_recent(tracked.clone());
            if let Some(call_id) = entry.call_id.clone()
                && !is_terminal(entry)
            {
                deduper.active_tools.insert(call_id, tracked);
            }
        }
        deduper
    }

    pub fn set_session_id(&mut self, session_id: String) {
        if self.next_sequence == 0 {
            self.session_id = session_id;
        }
    }

    pub fn accept(&mut self, mut candidate: NormalizedEntry, line_no: u64) -> NormalizedEntry {
        candidate.primary_text = normalize_text(&candidate.primary_text);
        candidate.secondary_text = normalize_text(&candidate.secondary_text);

        if let Some(candidate_source_id) = source_item_id(&candidate)
            && let Some(index) = self
                .recent
                .iter()
                .rposition(|tracked| source_item_id(&tracked.entry) == Some(candidate_source_id))
        {
            let mut tracked = self.recent.remove(index).expect("index came from recent");
            let previous_call_id = tracked.entry.call_id.clone();
            merge_entries(&mut tracked.entry, &candidate);
            tracked.line_no = line_no;
            let result = tracked.entry.clone();
            self.track_recent(tracked.clone());
            if let Some(call_id) = previous_call_id {
                self.active_tools.remove(&call_id);
            }
            if let Some(call_id) = result.call_id.clone()
                && !is_terminal(&result)
            {
                self.active_tools.insert(call_id, tracked);
            }
            return result;
        }

        if let Some(call_id) = candidate.call_id.clone() {
            if let Some(mut tracked) = self.active_tools.remove(&call_id) {
                merge_entries(&mut tracked.entry, &candidate);
                tracked.line_no = line_no;
                let result = tracked.entry.clone();
                self.track_recent(tracked.clone());
                if !is_terminal(&result) {
                    self.active_tools.insert(call_id, tracked);
                }
                return result;
            }
            if let Some(index) = self.recent.iter().position(|item| {
                item.entry.call_id.as_deref() == Some(call_id.as_str())
                    && line_no.saturating_sub(item.line_no) <= DEDUP_LINE_WINDOW
            }) {
                let mut tracked = self.recent.remove(index).expect("index came from recent");
                merge_entries(&mut tracked.entry, &candidate);
                tracked.line_no = line_no;
                let result = tracked.entry.clone();
                self.track_recent(tracked.clone());
                if !is_terminal(&result) {
                    self.active_tools.insert(call_id, tracked);
                }
                return result;
            }
        } else if let Some(index) = self.find_text_duplicate(&candidate, line_no) {
            let mut tracked = self.recent.remove(index).expect("index came from recent");
            merge_entries(&mut tracked.entry, &candidate);
            tracked.line_no = line_no;
            let result = tracked.entry.clone();
            self.track_recent(tracked);
            return result;
        }

        self.next_sequence = self.next_sequence.saturating_add(1);
        candidate.sequence = self.next_sequence;
        candidate.session_id.clone_from(&self.session_id);
        candidate.id = self.entry_id(&mut candidate);
        let tracked = TrackedEntry {
            line_no,
            entry: candidate.clone(),
        };
        self.track_recent(tracked.clone());
        if let Some(call_id) = candidate.call_id.clone()
            && !is_terminal(&candidate)
        {
            self.active_tools.insert(call_id, tracked);
        }
        candidate
    }

    fn find_text_duplicate(&self, candidate: &NormalizedEntry, line_no: u64) -> Option<usize> {
        self.recent.iter().rposition(|tracked| {
            let existing = &tracked.entry;
            if existing.kind != candidate.kind
                || existing.role != candidate.role
                || existing.phase != candidate.phase
            {
                return false;
            }
            let line_close = line_no.saturating_sub(tracked.line_no) <= DEDUP_LINE_WINDOW;
            let time_close = match (existing.timestamp_micros, candidate.timestamp_micros) {
                (Some(left), Some(right)) => {
                    left.abs_diff(right) <= DEDUP_TIME_WINDOW_MICROS as u64
                }
                _ => false,
            };
            if !line_close && !time_close {
                return false;
            }
            existing.primary_text == candidate.primary_text
                || is_streaming_prefix(&existing.primary_text, &candidate.primary_text)
        })
    }

    fn entry_id(&mut self, entry: &mut NormalizedEntry) -> String {
        let body = if entry.call_id.is_some() || entry.secondary_text.is_empty() {
            entry.primary_text.clone()
        } else {
            format!("{}\0{}", entry.primary_text, entry.secondary_text)
        };
        self.entry_id_with_body(entry, &body)
    }

    fn entry_id_with_body(&mut self, entry: &mut NormalizedEntry, body: &str) -> String {
        let body_hash = sha256(body.as_bytes());
        let base = format!(
            "{}\0{}\0{}\0{}\0{}",
            self.session_id,
            canonical_kind(entry),
            entry.timestamp_micros.unwrap_or_default(),
            entry.call_id.as_deref().unwrap_or_default(),
            body_hash
        );
        entry.id_basis.clone_from(&base);
        let occurrence = self.occurrences.entry(base.clone()).or_insert(0);
        let input = format!("{base}\0{occurrence}");
        *occurrence = occurrence.saturating_add(1);
        let digest = Sha256::digest(input.as_bytes());
        format!("e_{}", bytes_to_hex(&digest[..20]))
    }

    fn track_recent(&mut self, tracked: TrackedEntry) {
        if let Some(index) = self
            .recent
            .iter()
            .position(|item| item.entry.id == tracked.entry.id)
        {
            self.recent.remove(index);
        }
        let latest_line = tracked.line_no;
        let latest_time = tracked.entry.timestamp_micros;
        self.recent.push_back(tracked);
        while self.recent.front().is_some_and(|oldest| {
            let outside_line_window =
                latest_line.saturating_sub(oldest.line_no) > DEDUP_LINE_WINDOW;
            let outside_time_window = match (latest_time, oldest.entry.timestamp_micros) {
                (Some(left), Some(right)) => left.abs_diff(right) > DEDUP_TIME_WINDOW_MICROS as u64,
                _ => true,
            };
            outside_line_window && outside_time_window
        }) {
            self.recent.pop_front();
        }
    }
}

fn merge_entries(existing: &mut NormalizedEntry, candidate: &NormalizedEntry) {
    for raw_ref in &candidate.raw_refs {
        if !existing.raw_refs.contains(raw_ref) {
            existing.raw_refs.push(raw_ref.clone());
        }
    }

    let candidate_preferred = origin_rank(candidate.origin) > origin_rank(existing.origin);
    let candidate_is_final = is_streaming_prefix(&existing.primary_text, &candidate.primary_text)
        && candidate.primary_text.len() > existing.primary_text.len();
    if candidate_preferred {
        existing.kind = candidate.kind;
        existing.title.clone_from(&candidate.title);
        existing.primary_text.clone_from(&candidate.primary_text);
        existing
            .secondary_text
            .clone_from(&candidate.secondary_text);
        existing.presentation = candidate.presentation;
        existing.role = candidate.role;
        existing.phase = candidate.phase;
        existing.tool_kind = candidate.tool_kind;
        existing.tool_status = candidate.tool_status;
        existing.call_id.clone_from(&candidate.call_id);
        existing
            .parent_entry_id
            .clone_from(&candidate.parent_entry_id);
        existing.origin = candidate.origin;
        existing.searchable = candidate.searchable;
        existing.default_collapsed = candidate.default_collapsed;
    }
    if candidate_is_final {
        existing.primary_text.clone_from(&candidate.primary_text);
    }
    if existing.primary_text.is_empty() && !candidate.primary_text.is_empty() {
        existing.primary_text.clone_from(&candidate.primary_text);
    }
    if !candidate.secondary_text.is_empty() {
        if existing.secondary_text.is_empty() {
            existing
                .secondary_text
                .clone_from(&candidate.secondary_text);
        } else if !existing.secondary_text.contains(&candidate.secondary_text) {
            existing.secondary_text.push('\n');
            existing.secondary_text.push_str(&candidate.secondary_text);
        }
    }
    if candidate.tool_status.is_some() {
        existing.tool_status = candidate.tool_status;
    }
    if existing.title.is_empty() && !candidate.title.is_empty() {
        existing.title.clone_from(&candidate.title);
    }
    merge_metadata(existing, candidate);
}

fn merge_metadata(existing: &mut NormalizedEntry, candidate: &NormalizedEntry) {
    for (key, value) in &candidate.metadata {
        if is_attachment_count(key) {
            let merged = existing
                .metadata
                .get(key)
                .and_then(serde_json::Value::as_u64)
                .unwrap_or_default()
                .max(value.as_u64().unwrap_or_default());
            if merged > 0 {
                existing
                    .metadata
                    .insert(key.clone(), serde_json::Value::from(merged));
            }
        } else {
            existing.metadata.insert(key.clone(), value.clone());
        }
    }
}

fn is_attachment_count(key: &str) -> bool {
    matches!(
        key,
        "attachmentCount" | "imageAttachmentCount" | "audioAttachmentCount"
    )
}

fn source_item_id(entry: &NormalizedEntry) -> Option<&str> {
    entry
        .metadata
        .get("sourceItemId")
        .and_then(serde_json::Value::as_str)
}

const fn origin_rank(origin: EntryOrigin) -> u8 {
    match origin {
        EntryOrigin::ItemCompleted => 3,
        EntryOrigin::EventPresentation => 2,
        EntryOrigin::ResponseItem => 1,
        EntryOrigin::Derived => 0,
    }
}

fn is_terminal(entry: &NormalizedEntry) -> bool {
    use crate::model::ToolStatus::{Failed, Interrupted, Succeeded};
    matches!(entry.tool_status, Some(Succeeded | Failed | Interrupted))
}

fn is_streaming_prefix(left: &str, right: &str) -> bool {
    !left.is_empty() && !right.is_empty() && (left.starts_with(right) || right.starts_with(left))
}

pub fn normalize_text(value: &str) -> String {
    let normalized = value.replace("\r\n", "\n").replace('\r', "\n");
    normalized
        .lines()
        .map(str::trim_end)
        .collect::<Vec<_>>()
        .join("\n")
        .trim_end()
        .to_owned()
}

fn canonical_kind(entry: &NormalizedEntry) -> &'static str {
    use crate::model::EntryKind::{
        Context, Error, Marker, Message, Plan, Reasoning, Tool, Unknown, Warning,
    };
    match entry.kind {
        Message => "message",
        Reasoning => "reasoning",
        Tool => "tool",
        Plan => "plan",
        Context => "context",
        Marker => "marker",
        Warning => "warning",
        Error => "error",
        Unknown => "unknown",
    }
}

fn sha256(bytes: &[u8]) -> String {
    bytes_to_hex(&Sha256::digest(bytes))
}

fn bytes_to_hex(bytes: &[u8]) -> String {
    let mut output = String::with_capacity(bytes.len() * 2);
    for byte in bytes {
        use std::fmt::Write as _;
        write!(&mut output, "{byte:02x}").expect("writing to String cannot fail");
    }
    output
}

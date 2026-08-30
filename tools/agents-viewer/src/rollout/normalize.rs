use std::collections::{BTreeMap, HashMap, HashSet};
use std::io::{self, BufRead};

use chrono::{DateTime, NaiveDateTime, Utc};
use serde_json::Value;
use sha2::{Digest, Sha256};
use tokio_util::sync::CancellationToken;
use unicode_segmentation::UnicodeSegmentation;
use uuid::Uuid;

use crate::model::{
    Completeness, DiagnosticSeverity, EntryKind, EntryPresentation, IndexState, MessageRole, Phase,
    SessionParentRelation, SourceKind, ToolKind, ToolStatus,
};

use super::dedup::Deduper;
use super::envelope::Envelope;
use super::reader::{BoundedJsonlReader, LineReadStatus};
use super::types::{
    EntryOrigin, NormalizedEntry, ParseContext, ParseSeed, ParseSummary, ParsedRollout,
    ParserDiagnostic, ParserOutput, RawRecord, RootKind, SessionRecord,
};

mod content;
mod driver;
mod envelope;
mod event;
mod identity;
mod item;
mod message;
mod metadata;
mod session;
#[cfg(test)]
mod tests;

use content::*;
pub(crate) use driver::parse_catalog_rollout_cancellable;
pub use driver::{CollectingSink, ParseSink, parse_rollout};
pub(crate) use driver::{parse_rollout_cancellable, parse_rollout_from_seed_cancellable};
use envelope::*;
use event::*;
use identity::*;
use item::*;
use message::*;
use metadata::{
    add_agent_message_delivery_metadata, add_event_timing_metadata,
    add_execution_attribution_metadata, add_image_generation_failure_metadata,
    add_response_item_envelope_metadata, add_response_item_metadata, add_source_item_id,
    add_tool_capability_metadata, call_id, emit_diagnostic, item_id, parse_timestamp,
    payload_session_id, phase_field, role_field, session_id_from_file, source_item_id, source_kind,
    source_parent, status_field, string_option,
};
pub(crate) use metadata::{session_id_from_filename, timestamp_from_filename};
use session::*;

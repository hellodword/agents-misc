use super::*;

pub trait ParseSink {
    fn emit(&mut self, output: ParserOutput);
}

#[derive(Default)]
pub struct CollectingSink {
    raw_records: Vec<RawRecord>,
    entries: Vec<NormalizedEntry>,
    entry_indices: HashMap<String, usize>,
    diagnostics: Vec<ParserDiagnostic>,
}

impl CollectingSink {
    #[must_use]
    pub fn finish(self, summary: ParseSummary) -> ParsedRollout {
        ParsedRollout {
            summary,
            raw_records: self.raw_records,
            entries: self.entries,
            diagnostics: self.diagnostics,
        }
    }
}

impl ParseSink for CollectingSink {
    fn emit(&mut self, output: ParserOutput) {
        match output {
            ParserOutput::Raw(raw) => self.raw_records.push(raw),
            ParserOutput::Diagnostic(diagnostic) => self.diagnostics.push(diagnostic),
            ParserOutput::EntryUpsert(entry) => {
                if let Some(index) = self.entry_indices.get(&entry.id).copied() {
                    self.entries[index] = entry;
                } else {
                    self.entry_indices
                        .insert(entry.id.clone(), self.entries.len());
                    self.entries.push(entry);
                }
            }
        }
    }
}

pub fn parse_rollout<R: BufRead, S: ParseSink>(
    reader: R,
    context: &ParseContext,
    sink: &mut S,
) -> io::Result<ParseSummary> {
    parse_rollout_inner(reader, context, sink, None, None)
}

pub(crate) fn parse_rollout_cancellable<R: BufRead, S: ParseSink>(
    reader: R,
    context: &ParseContext,
    sink: &mut S,
    shutdown: &CancellationToken,
) -> io::Result<ParseSummary> {
    parse_rollout_inner(reader, context, sink, None, Some(shutdown))
}

pub(crate) fn parse_rollout_from_seed_cancellable<R: BufRead, S: ParseSink>(
    reader: R,
    context: &ParseContext,
    sink: &mut S,
    seed: ParseSeed,
    shutdown: &CancellationToken,
) -> io::Result<ParseSummary> {
    parse_rollout_inner(reader, context, sink, Some(seed), Some(shutdown))
}

fn parse_rollout_inner<R: BufRead, S: ParseSink>(
    reader: R,
    context: &ParseContext,
    sink: &mut S,
    seed: Option<ParseSeed>,
    shutdown: Option<&CancellationToken>,
) -> io::Result<ParseSummary> {
    let initial_session_id = session_id_from_file(context);
    let seed_next_sequence = seed.as_ref().map_or(0, |value| value.next_sequence);
    let mut session = seed.as_ref().map_or_else(
        || SessionBuilder::new(context, initial_session_id.clone()),
        |value| SessionBuilder::from_record(value.session.clone()),
    );
    if let Some(source_id) = seed.as_ref().and_then(|value| {
        value
            .recent
            .iter()
            .rev()
            .map(|(_, entry)| entry)
            .find(|entry| entry.title == "Inter-agent message")
            .and_then(source_item_id)
    }) {
        session.last_inter_agent_source_id = Some(source_id.to_owned());
    }
    let mut deduper = seed
        .as_ref()
        .map_or_else(|| Deduper::new(initial_session_id), Deduper::from_seed);
    let mut jsonl = match seed.as_ref() {
        Some(value) => BoundedJsonlReader::from_position(
            reader,
            context.max_event_bytes,
            value.checkpoint_line.saturating_add(1),
            value.checkpoint_offset,
        ),
        None => BoundedJsonlReader::new(reader, context.max_event_bytes),
    };
    let mut raw_record_count = seed.as_ref().map_or(0, |value| value.raw_record_count);
    let mut recognized_record_count = seed
        .as_ref()
        .map_or(0, |value| value.recognized_record_count);
    let mut incomplete_tail = false;
    let mut partial = seed.as_ref().is_some_and(|value| value.partial);
    let mut new_entry_ids = HashSet::new();

    loop {
        if shutdown.is_some_and(CancellationToken::is_cancelled) {
            return Err(io::Error::new(
                io::ErrorKind::Interrupted,
                "index scan cancelled",
            ));
        }
        let Some(line) = jsonl.read_next()? else {
            break;
        };
        raw_record_count = raw_record_count.saturating_add(1);
        if line.status == LineReadStatus::IncompleteTail {
            incomplete_tail = true;
            let raw_id = raw_ref_id(&session.id, line.byte_offset, line.byte_length);
            sink.emit(ParserOutput::Raw(RawRecord {
                id: raw_id.clone(),
                line_no: line.line_no,
                byte_offset: line.byte_offset,
                byte_length: line.byte_length,
                envelope_type: String::new(),
                parse_status: "incomplete_tail".into(),
                content_hash: line.content_hash,
                utf8: true,
                oversize: false,
                hex_preview: None,
            }));
            emit_diagnostic(
                sink,
                &mut session,
                DiagnosticSeverity::Info,
                "incomplete_tail",
                "trailing JSONL record is incomplete and will be retried",
                Some(line.line_no),
                Some(raw_id),
            );
            break;
        }

        if line.status == LineReadStatus::Oversize {
            partial = true;
            let raw_id = raw_ref_id(&session.id, line.byte_offset, line.byte_length);
            sink.emit(ParserOutput::Raw(RawRecord {
                id: raw_id.clone(),
                line_no: line.line_no,
                byte_offset: line.byte_offset,
                byte_length: line.byte_length,
                envelope_type: String::new(),
                parse_status: "oversize".into(),
                content_hash: line.content_hash,
                utf8: true,
                oversize: true,
                hex_preview: None,
            }));
            emit_diagnostic(
                sink,
                &mut session,
                DiagnosticSeverity::Warning,
                "content_too_large",
                "JSONL record exceeds configured event size limit",
                Some(line.line_no),
                Some(raw_id),
            );
            continue;
        }

        let bytes = line.bytes.expect("complete bounded line has bytes");
        if std::str::from_utf8(&bytes).is_err() {
            partial = true;
            let raw_id = raw_ref_id(&session.id, line.byte_offset, line.byte_length);
            sink.emit(ParserOutput::Raw(RawRecord {
                id: raw_id.clone(),
                line_no: line.line_no,
                byte_offset: line.byte_offset,
                byte_length: line.byte_length,
                envelope_type: String::new(),
                parse_status: "invalid_utf8".into(),
                content_hash: line.content_hash,
                utf8: false,
                oversize: false,
                hex_preview: Some(hex_preview(&bytes)),
            }));
            emit_diagnostic(
                sink,
                &mut session,
                DiagnosticSeverity::Warning,
                "invalid_utf8",
                "JSONL record is not valid UTF-8",
                Some(line.line_no),
                Some(raw_id),
            );
            continue;
        }

        let envelope = match Envelope::parse(&bytes) {
            Ok(envelope) => envelope,
            Err(_) => {
                partial = true;
                let raw_id = raw_ref_id(&session.id, line.byte_offset, line.byte_length);
                sink.emit(ParserOutput::Raw(RawRecord {
                    id: raw_id.clone(),
                    line_no: line.line_no,
                    byte_offset: line.byte_offset,
                    byte_length: line.byte_length,
                    envelope_type: String::new(),
                    parse_status: "invalid_json".into(),
                    content_hash: line.content_hash,
                    utf8: true,
                    oversize: false,
                    hex_preview: None,
                }));
                emit_diagnostic(
                    sink,
                    &mut session,
                    DiagnosticSeverity::Warning,
                    "invalid_json",
                    "JSONL record is not valid JSON",
                    Some(line.line_no),
                    Some(raw_id),
                );
                continue;
            }
        };

        if envelope.kind == "session_meta"
            && let Some(id) = payload_session_id(&envelope.payload)
        {
            session.id.clone_from(&id);
            deduper.set_session_id(id);
        }
        let raw_id = raw_ref_id(&session.id, line.byte_offset, line.byte_length);
        let known_envelope = is_known_envelope(&envelope.kind);
        let inherited =
            envelope.kind != "session_meta" && session.is_inherited_ordinal(envelope.ordinal);
        sink.emit(ParserOutput::Raw(RawRecord {
            id: raw_id.clone(),
            line_no: line.line_no,
            byte_offset: line.byte_offset,
            byte_length: line.byte_length,
            envelope_type: envelope.kind.clone(),
            parse_status: if inherited {
                "inherited"
            } else if known_envelope {
                "valid"
            } else {
                "unknown"
            }
            .into(),
            content_hash: line.content_hash,
            utf8: true,
            oversize: false,
            hex_preview: None,
        }));

        let timestamp_micros = envelope.timestamp.as_deref().and_then(parse_timestamp);
        if let Some(timestamp) = timestamp_micros {
            session.updated_at_micros = session.updated_at_micros.max(timestamp);
        } else if envelope.timestamp.is_some() {
            partial = true;
            emit_diagnostic(
                sink,
                &mut session,
                DiagnosticSeverity::Warning,
                "invalid_timestamp",
                "record timestamp is not valid RFC3339",
                Some(line.line_no),
                Some(raw_id.clone()),
            );
        }

        if envelope.kind == "session_meta"
            && envelope
                .payload
                .get("history_base")
                .is_some_and(|value| !value.is_null())
        {
            partial = true;
            emit_diagnostic(
                sink,
                &mut session,
                DiagnosticSeverity::Warning,
                "unsupported_history_base",
                "history_base references another rollout; only this rollout's local records are indexed",
                Some(line.line_no),
                Some(raw_id.clone()),
            );
        }

        if known_envelope {
            recognized_record_count = recognized_record_count.saturating_add(1);
        }
        if inherited {
            continue;
        }

        let normalized = normalize_envelope(
            &envelope,
            timestamp_micros,
            &raw_id,
            line.line_no,
            &mut session,
        );
        match normalized {
            NormalizeResult::None => {}
            NormalizeResult::Entry(candidate) => {
                let entry = deduper.accept(candidate, line.line_no);
                session.observe_entry(&entry);
                if entry.sequence > seed_next_sequence {
                    new_entry_ids.insert(entry.id.clone());
                }
                sink.emit(ParserOutput::EntryUpsert(entry));
            }
            NormalizeResult::Entries(candidates) => {
                for candidate in candidates {
                    let entry = deduper.accept(candidate, line.line_no);
                    session.observe_entry(&entry);
                    if entry.sequence > seed_next_sequence {
                        new_entry_ids.insert(entry.id.clone());
                    }
                    sink.emit(ParserOutput::EntryUpsert(entry));
                }
            }
            NormalizeResult::Unknown(candidate, code) => {
                partial = true;
                let entry = deduper.accept(candidate, line.line_no);
                if entry.sequence > seed_next_sequence {
                    new_entry_ids.insert(entry.id.clone());
                }
                sink.emit(ParserOutput::EntryUpsert(entry));
                emit_diagnostic(
                    sink,
                    &mut session,
                    DiagnosticSeverity::Warning,
                    code,
                    "record type is not supported; raw metadata remains available",
                    Some(line.line_no),
                    Some(raw_id),
                );
            }
        }
    }

    let new_entry_count = new_entry_ids.len() as u64;
    session.entry_count = session.entry_count.saturating_add(new_entry_count);
    let checkpoint = jsonl.stable_prefix();
    Ok(ParseSummary {
        session: session.finish(context, recognized_record_count, partial, incomplete_tail),
        raw_record_count,
        recognized_record_count,
        incomplete_tail,
        stable_prefix_bytes: checkpoint.offset,
        stable_prefix_hash: checkpoint.prefix_hash,
    })
}

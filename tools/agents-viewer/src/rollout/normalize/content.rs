use super::*;

pub(super) fn string_field(payload: &Value, keys: &[&str]) -> String {
    keys.iter()
        .find_map(|key| value_text(payload.get(*key)))
        .unwrap_or_default()
}

pub(super) fn content_text(value: Option<&Value>) -> String {
    value_text(value).unwrap_or_default()
}

pub(super) fn user_input_text(value: Option<&Value>) -> String {
    let Some(items) = value.and_then(Value::as_array) else {
        return content_text(value);
    };
    items
        .iter()
        .filter_map(|item| {
            let kind = item.get("type").and_then(Value::as_str).unwrap_or_default();
            match kind {
                "text" | "input_text" => {
                    item.get("text").and_then(Value::as_str).map(str::to_owned)
                }
                "skill" => item
                    .get("name")
                    .and_then(Value::as_str)
                    .map(|name| format!("[skill: {name}]")),
                "mention" => item
                    .get("name")
                    .and_then(Value::as_str)
                    .map(|name| format!("[mention: {name}]")),
                _ => None,
            }
        })
        .collect::<Vec<_>>()
        .join("\n")
}

pub(super) fn string_array(value: Option<&Value>, separator: &str) -> String {
    match value {
        Some(Value::Array(values)) => values
            .iter()
            .filter_map(Value::as_str)
            .collect::<Vec<_>>()
            .join(separator),
        Some(Value::String(value)) => value.clone(),
        _ => String::new(),
    }
}

pub(super) fn joined_fields(payload: &Value, keys: &[&str]) -> String {
    keys.iter()
        .filter_map(|key| payload.get(*key))
        .filter_map(display_value)
        .filter(|text| !text.is_empty())
        .collect::<Vec<_>>()
        .join("\n")
}

pub(super) fn output_text(value: Option<&Value>) -> String {
    value.and_then(display_value).unwrap_or_default()
}

pub(super) fn display_value(value: &Value) -> Option<String> {
    match value {
        Value::Null => None,
        Value::String(text) => (!is_media_data_uri(text)).then(|| text.clone()),
        Value::Number(number) => Some(number.to_string()),
        Value::Bool(value) => Some(value.to_string()),
        Value::Array(items) => {
            let typed_content = items.iter().any(|item| {
                item.get("type")
                    .and_then(Value::as_str)
                    .is_some_and(is_content_item_kind)
            });
            if typed_content {
                value_text(Some(value))
            } else if items.iter().all(Value::is_string) {
                Some(
                    items
                        .iter()
                        .filter_map(Value::as_str)
                        .collect::<Vec<_>>()
                        .join("\n"),
                )
            } else {
                pretty_sanitized(value)
            }
        }
        Value::Object(object) => {
            if let Some(content) = object
                .get("content")
                .or_else(|| object.get("content_items"))
                .or_else(|| object.get("contentItems"))
            {
                let text = output_text(Some(content));
                return (!text.is_empty()).then_some(text);
            }
            object
                .get("message")
                .and_then(Value::as_str)
                .map(str::to_owned)
                .or_else(|| pretty_sanitized(value))
        }
    }
}

pub(super) fn pretty_sanitized(value: &Value) -> Option<String> {
    let sanitized = sanitize_for_display(value);
    (!sanitized.is_null())
        .then(|| serde_json::to_string_pretty(&sanitized).ok())
        .flatten()
}

pub(super) fn sanitize_for_display(value: &Value) -> Value {
    match value {
        Value::Array(items) => Value::Array(items.iter().map(sanitize_for_display).collect()),
        Value::Object(object) => {
            let attachment_payload =
                object
                    .get("type")
                    .and_then(Value::as_str)
                    .is_some_and(|kind| {
                        let kind = kind.to_ascii_lowercase();
                        kind.contains("image")
                            || kind.contains("audio")
                            || kind.contains("encrypted")
                    });
            let sanitized = object
                .iter()
                .filter(|(key, _)| {
                    !(matches!(
                        key.as_str(),
                        "image_url"
                            | "imageUrl"
                            | "audio_url"
                            | "audioUrl"
                            | "encrypted_content"
                            | "encryptedContent"
                    ) || attachment_payload
                        && matches!(key.as_str(), "data" | "blob" | "base64" | "b64_json"))
                })
                .map(|(key, value)| (key.clone(), sanitize_for_display(value)))
                .collect();
            Value::Object(sanitized)
        }
        other => other.clone(),
    }
}

pub(super) fn is_content_item_kind(kind: &str) -> bool {
    let kind = kind.to_ascii_lowercase();
    kind.contains("text")
        || kind.contains("image")
        || kind.contains("audio")
        || kind.contains("encrypted")
}

pub(super) fn is_media_data_uri(value: &str) -> bool {
    let value = value.trim_start();
    ["data:image/", "data:audio/", "data:video/"]
        .iter()
        .any(|prefix| {
            value
                .get(..prefix.len())
                .is_some_and(|candidate| candidate.eq_ignore_ascii_case(prefix))
        })
}

pub(super) fn value_text(value: Option<&Value>) -> Option<String> {
    match value? {
        Value::String(text) => Some(text.clone()),
        Value::Array(items) => {
            let parts = items
                .iter()
                .filter_map(|item| {
                    if item
                        .get("type")
                        .and_then(Value::as_str)
                        .is_some_and(|kind| {
                            let kind = kind.to_ascii_lowercase();
                            kind.contains("image")
                                || kind.contains("audio")
                                || kind.contains("encrypted")
                        })
                    {
                        None
                    } else {
                        item.get("text")
                            .and_then(Value::as_str)
                            .map(str::to_owned)
                            .or_else(|| item.as_str().map(str::to_owned))
                    }
                })
                .collect::<Vec<_>>();
            (!parts.is_empty()).then(|| parts.join("\n"))
        }
        Value::Number(number) => Some(number.to_string()),
        Value::Bool(value) => Some(value.to_string()),
        _ => None,
    }
}

pub(super) fn pretty_value(value: Option<&Value>) -> String {
    value
        .filter(|value| !value.is_null())
        .and_then(|value| serde_json::to_string_pretty(value).ok())
        .unwrap_or_default()
}

pub(super) fn plan_text(payload: &Value) -> String {
    payload
        .get("plan")
        .and_then(Value::as_array)
        .map(|items| {
            items
                .iter()
                .filter_map(|item| item.get("step").and_then(Value::as_str))
                .collect::<Vec<_>>()
                .join("\n")
        })
        .filter(|text| !text.is_empty())
        .unwrap_or_else(|| string_field(payload, &["message", "delta"]))
}

pub(super) fn add_attachment_metadata(entry: &mut NormalizedEntry, payload: &Value) {
    let (image_count, audio_count) = attachment_counts(payload);
    add_attachment_counts(entry, image_count, audio_count);
}

pub(super) fn attachment_counts(payload: &Value) -> (usize, usize) {
    let mut image_count = ["images", "local_images", "localImages"]
        .iter()
        .filter_map(|key| payload.get(*key).and_then(Value::as_array))
        .map(Vec::len)
        .sum();
    let mut audio_count = [
        "audio",
        "local_audio",
        "localAudio",
        "audios",
        "local_audios",
        "localAudios",
    ]
    .iter()
    .filter_map(|key| payload.get(*key).and_then(Value::as_array))
    .map(Vec::len)
    .sum();

    for items in ["content", "content_items", "contentItems"]
        .iter()
        .filter_map(|key| payload.get(*key).and_then(Value::as_array))
    {
        let (content_images, content_audio) = content_attachment_counts(items);
        image_count += content_images;
        audio_count += content_audio;
    }

    for nested in ["output", "result", "Ok", "ok"]
        .iter()
        .filter_map(|key| payload.get(*key))
    {
        let (nested_images, nested_audio) = if let Some(items) = nested.as_array() {
            content_attachment_counts(items)
        } else if nested.is_object() {
            attachment_counts(nested)
        } else {
            (0, 0)
        };
        image_count += nested_images;
        audio_count += nested_audio;
    }
    (image_count, audio_count)
}

pub(super) fn content_attachment_counts(items: &[Value]) -> (usize, usize) {
    let mut image_count = 0;
    let mut audio_count = 0;
    for item in items {
        let kind = item
            .get("type")
            .and_then(Value::as_str)
            .unwrap_or_default()
            .to_ascii_lowercase();
        if kind.contains("image") {
            image_count += 1;
        } else if kind.contains("audio") {
            audio_count += 1;
        }
    }
    (image_count, audio_count)
}

pub(super) fn add_attachment_counts(
    entry: &mut NormalizedEntry,
    image_count: usize,
    audio_count: usize,
) {
    let attachment_count = image_count.saturating_add(audio_count);
    if attachment_count > 0 {
        entry
            .metadata
            .insert("attachmentCount".into(), Value::from(attachment_count));
    }
    if image_count > 0 {
        entry
            .metadata
            .insert("imageAttachmentCount".into(), Value::from(image_count));
    }
    if audio_count > 0 {
        entry
            .metadata
            .insert("audioAttachmentCount".into(), Value::from(audio_count));
    }
}

pub(super) fn has_encrypted_content(payload: &Value) -> bool {
    payload
        .get("encrypted_content")
        .or_else(|| payload.get("encryptedContent"))
        .is_some_and(|value| !value.is_null())
        || ["content", "content_items", "contentItems"]
            .iter()
            .filter_map(|key| payload.get(*key).and_then(Value::as_array))
            .flatten()
            .any(|item| {
                item.get("type")
                    .and_then(Value::as_str)
                    .is_some_and(|kind| kind.to_ascii_lowercase().contains("encrypted"))
                    || item
                        .get("encrypted_content")
                        .or_else(|| item.get("encryptedContent"))
                        .is_some_and(|value| !value.is_null())
            })
}

pub(super) fn truncate_graphemes(value: &str, max: usize) -> String {
    value.graphemes(true).take(max).collect()
}

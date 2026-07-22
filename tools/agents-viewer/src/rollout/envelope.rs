use serde_json::Value;

#[derive(Clone, Debug)]
pub struct Envelope {
    pub timestamp: Option<String>,
    pub ordinal: Option<u64>,
    pub kind: String,
    pub payload: Value,
}

impl Envelope {
    pub fn parse(bytes: &[u8]) -> Result<Self, serde_json::Error> {
        let value: Value = serde_json::from_slice(bytes)?;
        let Some(object) = value.as_object() else {
            return Ok(Self {
                timestamp: None,
                ordinal: None,
                kind: String::new(),
                payload: Value::Null,
            });
        };
        Ok(Self {
            timestamp: object
                .get("timestamp")
                .and_then(Value::as_str)
                .map(str::to_owned),
            ordinal: object.get("ordinal").and_then(Value::as_u64),
            kind: object
                .get("type")
                .and_then(Value::as_str)
                .unwrap_or_default()
                .to_owned(),
            payload: object.get("payload").cloned().unwrap_or(Value::Null),
        })
    }
}

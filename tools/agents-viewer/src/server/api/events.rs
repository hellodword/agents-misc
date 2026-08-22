use super::*;

pub(super) async fn events(
    State(state): State<AppState>,
    headers: http::HeaderMap,
) -> Result<Response, ApiFailure> {
    let last = last_event_id(&headers)?;
    Ok(state.sse.subscribe(last).await?.into_response())
}

fn last_event_id(headers: &http::HeaderMap) -> Result<Option<u64>, ApiFailure> {
    let last = headers
        .get("last-event-id")
        .map(|value| {
            value
                .to_str()
                .map_err(|_| ApiFailure::invalid("Last-Event-ID is invalid"))
        })
        .transpose()?
        .map(|value| {
            value
                .parse::<u64>()
                .map_err(|_| ApiFailure::invalid("Last-Event-ID must be an integer"))
        })
        .transpose()?;
    Ok(last)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn last_event_id_is_optional_and_strictly_numeric() {
        assert_eq!(last_event_id(&http::HeaderMap::new()).unwrap(), None);
        let mut headers = http::HeaderMap::new();
        headers.insert("last-event-id", "42".parse().unwrap());
        assert_eq!(last_event_id(&headers).unwrap(), Some(42));
        headers.insert("last-event-id", "forty-two".parse().unwrap());
        assert!(last_event_id(&headers).is_err());
    }
}

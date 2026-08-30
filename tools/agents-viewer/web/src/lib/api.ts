import type {
  ApiErrorEnvelope,
  ContentChunk,
  EntryListItem,
  RawRecord,
  RawRecordSummary,
  SearchHit,
  SessionDetail,
  SessionGroup,
  SessionSummary,
  SseEventPayload,
  SseEventType,
  Status,
  TranscriptEntry,
} from "@/generated/api";

export type Page<T> = {
  data: T[];
  nextCursor?: string;
  previousCursor?: string;
  partial: boolean;
};

export class ApiClientError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
  }
}

async function get<T>(path: string, signal?: AbortSignal): Promise<T> {
  const response = await fetch(path, {
    signal,
    headers: { accept: "application/json" },
  });
  const body = (await response.json()) as T | ApiErrorEnvelope;
  if (!response.ok) {
    const failure = body as ApiErrorEnvelope;
    throw new ApiClientError(
      response.status,
      failure.error.code,
      failure.error.message,
    );
  }
  return body as T;
}

async function liveSync(
  id: string,
  signal: AbortSignal,
  onAccepted: () => void,
): Promise<void> {
  const response = await fetch(
    `/api/v1/sessions/${encodeURIComponent(id)}/live-sync`,
    {
      method: "POST",
      signal,
      headers: { accept: "text/event-stream" },
    },
  );
  if (!response.ok) {
    const failure = (await response.json()) as ApiErrorEnvelope;
    throw new ApiClientError(
      response.status,
      failure.error.code,
      failure.error.message,
    );
  }
  onAccepted();
  const reader = response.body?.getReader();
  if (!reader) throw new Error("live sync response has no body");
  try {
    while (!(await reader.read()).done) {
      // Reading the stream owns the server-side lease. Snapshot notifications
      // arrive on the application's shared SSE connection.
    }
  } finally {
    reader.releaseLock();
  }
}

const query = (
  values: Record<string, string | number | boolean | undefined>,
) => {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(values))
    if (value !== undefined && value !== false && value !== "")
      params.set(key, String(value));
  const encoded = params.toString();
  return encoded ? `?${encoded}` : "";
};

export const api = {
  status: (signal?: AbortSignal) => get<Status>("/api/v1/status", signal),
  sessions: (
    options: {
      archived?: string;
      source?: string;
      cwd?: string;
      cursor?: string;
      limit?: number;
    },
    signal?: AbortSignal,
  ) => get<Page<SessionSummary>>(`/api/v1/sessions${query(options)}`, signal),
  sessionGroups: (
    options: {
      archived?: string;
      source?: string;
      cwd?: string;
      cursor?: string;
      limit?: number;
    },
    signal?: AbortSignal,
  ) =>
    get<Page<SessionGroup>>(`/api/v1/session-groups${query(options)}`, signal),
  session: (id: string, signal?: AbortSignal) =>
    get<SessionDetail>(`/api/v1/sessions/${encodeURIComponent(id)}`, signal),
  liveSync,
  entries: (
    id: string,
    options: {
      cursor?: string;
      aroundEntryId?: string;
      direction?: string;
      limit?: number;
      includeTechnical?: boolean;
      displayTypes?: string;
    },
    signal?: AbortSignal,
  ) =>
    get<Page<EntryListItem>>(
      `/api/v1/sessions/${encodeURIComponent(id)}/entries${query(options)}`,
      signal,
    ),
  entry: (sessionId: string, entryId: string, signal?: AbortSignal) =>
    get<TranscriptEntry>(
      `/api/v1/sessions/${encodeURIComponent(sessionId)}/entries/${encodeURIComponent(entryId)}`,
      signal,
    ),
  content: (
    sessionId: string,
    entryId: string,
    field: "primary" | "secondary",
    offset = 0,
    signal?: AbortSignal,
  ) =>
    get<ContentChunk>(
      `/api/v1/sessions/${encodeURIComponent(sessionId)}/entries/${encodeURIComponent(entryId)}/content${query({ field, offset })}`,
      signal,
    ),
  rawList: (sessionId: string, cursor?: string, signal?: AbortSignal) =>
    get<Page<RawRecordSummary>>(
      `/api/v1/sessions/${encodeURIComponent(sessionId)}/raw${query({ cursor })}`,
      signal,
    ),
  raw: (sessionId: string, rawId: string, offset = 0, signal?: AbortSignal) =>
    get<RawRecord>(
      `/api/v1/sessions/${encodeURIComponent(sessionId)}/raw/${encodeURIComponent(rawId)}${query({ offset })}`,
      signal,
    ),
  search: (
    q: string,
    options: {
      archived?: string;
      source?: string;
      kind?: string;
      session?: string;
      allTypes?: boolean;
    } = {},
    signal?: AbortSignal,
  ) =>
    get<Page<SearchHit>>(`/api/v1/search${query({ q, ...options })}`, signal),
};

export type LiveEvent = {
  type: Exclude<SseEventType, "resync">;
  data: SseEventPayload;
};

export function subscribeEvents(
  onEvent: (event: LiveEvent) => void,
  onResync: () => void,
) {
  const source = new EventSource("/api/v1/events");
  for (const name of [
    "catalogProgress",
    "catalogUpdated",
    "snapshotUpdated",
    "liveSyncStateChanged",
    "diagnostic",
    "heartbeat",
  ] as const) {
    source.addEventListener(name, (event) => {
      try {
        onEvent({
          type: name,
          data: JSON.parse((event as MessageEvent).data) as SseEventPayload,
        });
      } catch {
        onResync();
      }
    });
  }
  source.addEventListener("resync", onResync);
  return () => source.close();
}

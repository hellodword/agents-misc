mod dedup;
mod envelope;
pub(crate) mod normalize;
mod reader;
mod types;

pub use normalize::{CollectingSink, ParseSink, parse_rollout};
pub use reader::{
    BoundedJsonlReader, FileCheckpoint, LineReadStatus, ReadLine, checkpoint_for_file,
    verify_checkpoint,
};
pub(crate) use types::ParseSeed;
pub use types::{
    EntryOrigin, NormalizedEntry, PARSER_VERSION, ParseContext, ParseSummary, ParsedRollout,
    ParserDiagnostic, ParserOutput, RawRecord, RootKind, SessionRecord,
};

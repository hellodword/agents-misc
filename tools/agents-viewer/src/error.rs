use std::path::PathBuf;

#[derive(Debug, thiserror::Error)]
pub enum ViewerError {
    #[error("invalid argument: {0}")]
    InvalidArgument(String),

    #[error("failed to read or write {path}: {source}")]
    Io {
        path: PathBuf,
        #[source]
        source: std::io::Error,
    },

    #[error("generated TypeScript contract differs from {0}")]
    GeneratedContractOutOfDate(PathBuf),

    #[error("Codex source home does not exist or is not a directory: {0}")]
    SourceHomeUnavailable(PathBuf),

    #[error("source and cache paths overlap: source={source_path}, cache={cache}")]
    PathOverlap {
        source_path: PathBuf,
        cache: PathBuf,
    },

    #[error("unsafe cache permissions for {path}: {reason}")]
    UnsafeCachePermissions { path: PathBuf, reason: String },

    #[error("cache is already locked by another agents-viewer process: {0}")]
    CacheLocked(PathBuf),

    #[error("source path is a symbolic link: {0}")]
    SourceSymlink(PathBuf),

    #[error("source path escapes its allowed root: {0}")]
    SourceOutsideRoot(PathBuf),

    #[error("source changed while it was being opened: {0}")]
    SourceChanged(PathBuf),
}

pub type Result<T> = std::result::Result<T, ViewerError>;

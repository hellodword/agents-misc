use std::io::Write as _;
use std::net::SocketAddr;
use std::path::{Path, PathBuf};

use schemars::JsonSchema;
use serde::{Deserialize, Serialize};
use tempfile::NamedTempFile;

use crate::cli::Cli;
use crate::paths::{
    CachePaths, SourceRoots, canonicalize_allow_missing, resolve_cache_paths, resolve_source_roots,
};
use crate::permissions::validate_cache_file;
use crate::{Result, ViewerError};

pub const DEFAULT_MAX_EVENT_BYTES: usize = 32 * 1024 * 1024;
const MIN_EVENT_BYTES: usize = 1024 * 1024;
const MAX_EVENT_BYTES: usize = 256 * 1024 * 1024;
const CONFIG_FILE_NAME: &str = "config.toml";
const SCHEMA_FILE_NAME: &str = "schema.json";

pub const DEFAULT_CONFIG_TOML: &str = r##"#:schema ./schema.json

# Codex data source. Only sessions/ and archived_sessions/ are read.
source_dir = "~/.codex"

# Viewer-owned configuration, index, and lock root.
data_dir = "~/.agents-viewer"

# Fixed bootstrap window: 7 = seven days, -1 = all history, 0 = new sessions only.
initial_index_days = 7

# HTTP must bind to an IPv4 or IPv6 loopback address. Port 0 selects a free port.
listen = "127.0.0.1:4747"

# Maximum complete JSONL record size. Units: B, KiB, MiB, or GiB.
max_event_bytes = "32MiB"

# Diagnostic verbosity: trace, debug, info, warn, error, or off.
log_level = "warn"
"##;

#[derive(Clone, Copy, Debug, Default, Deserialize, Eq, JsonSchema, PartialEq, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum LogLevel {
    Trace,
    Debug,
    Info,
    #[default]
    Warn,
    Error,
    Off,
}

impl LogLevel {
    #[must_use]
    pub const fn as_filter(self) -> &'static str {
        match self {
            Self::Trace => "trace",
            Self::Debug => "debug",
            Self::Info => "info",
            Self::Warn => "warn",
            Self::Error => "error",
            Self::Off => "off",
        }
    }
}

#[derive(Clone, Debug, Deserialize, JsonSchema, Serialize)]
#[serde(default, deny_unknown_fields)]
pub struct FileConfig {
    /// Codex data source. Only sessions/ and archived_sessions/ are read.
    pub source_dir: String,
    /// Viewer-owned configuration, index, and lock root.
    pub data_dir: String,
    /// Fixed bootstrap window: 7 = seven days, -1 = all history, 0 = new sessions only.
    pub initial_index_days: i64,
    /// IPv4 or IPv6 loopback listen address. Port 0 selects a free port.
    pub listen: String,
    /// Maximum complete JSONL record size using B, KiB, MiB, or GiB.
    pub max_event_bytes: String,
    /// Diagnostic verbosity.
    pub log_level: LogLevel,
}

impl Default for FileConfig {
    fn default() -> Self {
        Self {
            source_dir: "~/.codex".into(),
            data_dir: "~/.agents-viewer".into(),
            initial_index_days: 7,
            listen: "127.0.0.1:4747".into(),
            max_event_bytes: "32MiB".into(),
            log_level: LogLevel::Warn,
        }
    }
}

#[derive(Clone, Debug)]
pub struct Config {
    pub config_path: PathBuf,
    pub roots: SourceRoots,
    pub cache: CachePaths,
    pub listen: SocketAddr,
    pub rebuild_index: bool,
    pub initial_index_days: i64,
    pub max_event_bytes: usize,
    pub log_level: LogLevel,
}

impl Config {
    pub fn load(cli: Cli) -> Result<Self> {
        let home = current_home()?;
        let config_path = cli
            .config
            .unwrap_or_else(|| home.join(".agents-viewer").join(CONFIG_FILE_NAME));
        let config_path = absolute_from_current_dir(&config_path)?;
        let created = ensure_config(&config_path)?;
        write_schema(&config_path)?;
        if created {
            eprintln!(
                "agents-viewer: created configuration at {}",
                config_path.display()
            );
        }

        validate_regular_private_file(&config_path)?;
        let contents = std::fs::read_to_string(&config_path).map_err(|source| ViewerError::Io {
            path: config_path.clone(),
            source,
        })?;
        let file = toml::from_str::<FileConfig>(&contents).map_err(|error| {
            ViewerError::InvalidArgument(format!(
                "invalid configuration {}: {error}",
                config_path.display()
            ))
        })?;
        if file.initial_index_days < -1 {
            return Err(ViewerError::InvalidArgument(
                "initial_index_days must be -1 or a non-negative integer".into(),
            ));
        }
        let listen = parse_listen(&file.listen).map_err(ViewerError::InvalidArgument)?;
        let max_event_bytes =
            parse_event_bytes(&file.max_event_bytes).map_err(ViewerError::InvalidArgument)?;
        let config_dir = config_path.parent().ok_or_else(|| {
            ViewerError::InvalidArgument(format!(
                "configuration path has no parent: {}",
                config_path.display()
            ))
        })?;
        let source_dir = resolve_config_path(&file.source_dir, config_dir, &home)?;
        let data_dir = resolve_config_path(&file.data_dir, config_dir, &home)?;
        let roots = resolve_source_roots(&source_dir)?;
        let cache = resolve_cache_paths(&roots.home, &data_dir)?;
        Ok(Self {
            config_path,
            roots,
            cache,
            listen,
            rebuild_index: cli.rebuild_index,
            initial_index_days: file.initial_index_days,
            max_event_bytes,
            log_level: file.log_level,
        })
    }
}

#[must_use]
pub fn schema_json() -> String {
    let schema = schemars::schema_for!(FileConfig);
    let mut output = serde_json::to_string_pretty(&schema).expect("JSON schema is serializable");
    output.push('\n');
    output
}

fn ensure_config(path: &Path) -> Result<bool> {
    if path.exists() {
        return Ok(false);
    }
    let parent = path.parent().ok_or_else(|| {
        ViewerError::InvalidArgument(format!(
            "configuration path has no parent: {}",
            path.display()
        ))
    })?;
    create_private_parent(parent)?;
    let mut temporary = NamedTempFile::new_in(parent).map_err(|source| ViewerError::Io {
        path: parent.to_path_buf(),
        source,
    })?;
    temporary
        .write_all(DEFAULT_CONFIG_TOML.as_bytes())
        .and_then(|()| temporary.as_file().sync_all())
        .map_err(|source| ViewerError::Io {
            path: path.to_path_buf(),
            source,
        })?;
    match temporary.persist_noclobber(path) {
        Ok(_) => Ok(true),
        Err(error) if error.error.kind() == std::io::ErrorKind::AlreadyExists => Ok(false),
        Err(error) => Err(ViewerError::Io {
            path: path.to_path_buf(),
            source: error.error,
        }),
    }
}

fn write_schema(config_path: &Path) -> Result<()> {
    let parent = config_path.parent().ok_or_else(|| {
        ViewerError::InvalidArgument(format!(
            "configuration path has no parent: {}",
            config_path.display()
        ))
    })?;
    let path = parent.join(SCHEMA_FILE_NAME);
    let expected = schema_json();
    if std::fs::read(&path).is_ok_and(|current| current == expected.as_bytes()) {
        validate_regular_private_file(&path)?;
        return Ok(());
    }
    let mut temporary = NamedTempFile::new_in(parent).map_err(|source| ViewerError::Io {
        path: parent.to_path_buf(),
        source,
    })?;
    temporary
        .write_all(expected.as_bytes())
        .and_then(|()| temporary.as_file().sync_all())
        .map_err(|source| ViewerError::Io {
            path: path.clone(),
            source,
        })?;
    temporary.persist(&path).map_err(|error| ViewerError::Io {
        path: path.clone(),
        source: error.error,
    })?;
    validate_regular_private_file(&path)
}

fn create_private_parent(path: &Path) -> Result<()> {
    if path.exists() {
        if path.is_dir() {
            return Ok(());
        }
        return Err(ViewerError::InvalidArgument(format!(
            "configuration parent is not a directory: {}",
            path.display()
        )));
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::DirBuilderExt as _;
        std::fs::DirBuilder::new()
            .recursive(true)
            .mode(0o700)
            .create(path)
            .map_err(|source| ViewerError::Io {
                path: path.to_path_buf(),
                source,
            })?;
    }
    #[cfg(not(unix))]
    std::fs::create_dir_all(path).map_err(|source| ViewerError::Io {
        path: path.to_path_buf(),
        source,
    })?;
    Ok(())
}

fn validate_regular_private_file(path: &Path) -> Result<()> {
    let metadata = std::fs::symlink_metadata(path).map_err(|source| ViewerError::Io {
        path: path.to_path_buf(),
        source,
    })?;
    if metadata.file_type().is_symlink() || !metadata.is_file() {
        return Err(ViewerError::InvalidArgument(format!(
            "configuration asset must be a regular file: {}",
            path.display()
        )));
    }
    validate_cache_file(path)
}

fn current_home() -> Result<PathBuf> {
    dirs::home_dir().ok_or_else(|| {
        ViewerError::InvalidArgument("current user home directory is unavailable".into())
    })
}

fn absolute_from_current_dir(path: &Path) -> Result<PathBuf> {
    if path.is_absolute() {
        return Ok(path.to_path_buf());
    }
    std::env::current_dir()
        .map(|current| current.join(path))
        .map_err(|source| ViewerError::Io {
            path: PathBuf::from("."),
            source,
        })
}

fn resolve_config_path(value: &str, config_dir: &Path, home: &Path) -> Result<PathBuf> {
    let expanded = if value == "~" {
        home.to_path_buf()
    } else if let Some(relative) = value.strip_prefix("~/") {
        home.join(relative)
    } else if value.starts_with('~') {
        return Err(ViewerError::InvalidArgument(format!(
            "only ~ and ~/... current-user paths are supported: {value}"
        )));
    } else {
        let path = Path::new(value);
        if path.is_absolute() {
            path.to_path_buf()
        } else {
            config_dir.join(path)
        }
    };
    canonicalize_allow_missing(&expanded)
}

pub fn parse_listen(value: &str) -> std::result::Result<SocketAddr, String> {
    let address = value.parse::<SocketAddr>().map_err(|_| {
        "listen must be an IP address and port (hostnames are not accepted)".to_owned()
    })?;
    if !address.ip().is_loopback() {
        return Err("listen address must be IPv4 or IPv6 loopback".into());
    }
    Ok(address)
}

pub fn parse_event_bytes(value: &str) -> std::result::Result<usize, String> {
    let split = value
        .find(|character: char| !character.is_ascii_digit())
        .unwrap_or(value.len());
    let number = value[..split]
        .parse::<u64>()
        .map_err(|_| "size must start with a base-10 integer".to_owned())?;
    let multiplier = match &value[split..] {
        "B" => 1,
        "KiB" => 1024,
        "MiB" => 1024 * 1024,
        "GiB" => 1024 * 1024 * 1024,
        _ => return Err("size suffix must be B, KiB, MiB, or GiB".into()),
    };
    let bytes = number
        .checked_mul(multiplier)
        .and_then(|value| usize::try_from(value).ok())
        .ok_or_else(|| "size exceeds this platform's range".to_owned())?;
    if !(MIN_EVENT_BYTES..=MAX_EVENT_BYTES).contains(&bytes) {
        return Err("size must be between 1MiB and 256MiB".into());
    }
    Ok(bytes)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn default_config_is_created_once_and_schema_is_regenerated_separately() {
        let temp = tempfile::TempDir::new_in(".").unwrap();
        let config = temp.path().join("settings/config.toml");
        assert!(ensure_config(&config).unwrap());
        assert!(!ensure_config(&config).unwrap());
        let original = std::fs::read_to_string(&config).unwrap();
        assert!(original.starts_with("#:schema ./schema.json\n"));
        for field in [
            "source_dir",
            "data_dir",
            "initial_index_days",
            "listen",
            "max_event_bytes",
            "log_level",
        ] {
            assert!(original.contains(field));
        }
        write_schema(&config).unwrap();
        std::fs::write(config.parent().unwrap().join("schema.json"), b"stale").unwrap();
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt as _;
            std::fs::set_permissions(
                config.parent().unwrap().join("schema.json"),
                std::fs::Permissions::from_mode(0o600),
            )
            .unwrap();
        }
        write_schema(&config).unwrap();
        assert_eq!(std::fs::read_to_string(&config).unwrap(), original);
        let schema: serde_json::Value = serde_json::from_str(
            &std::fs::read_to_string(config.parent().unwrap().join("schema.json")).unwrap(),
        )
        .unwrap();
        assert_eq!(schema["additionalProperties"], false);
    }

    #[test]
    fn configured_scalar_validation_is_strict() {
        assert!(parse_listen("127.0.0.1:0").is_ok());
        assert!(parse_listen("0.0.0.0:4747").is_err());
        assert_eq!(parse_event_bytes("32MiB").unwrap(), 32 * 1024 * 1024);
        assert!(parse_event_bytes("32MB").is_err());
    }
}

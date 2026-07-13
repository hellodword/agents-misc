use std::path::PathBuf;

use clap::Parser;

#[derive(Clone, Debug, Parser)]
#[command(
    name = "agents-viewer",
    version,
    about = "Read-only local Codex session viewer"
)]
pub struct Cli {
    /// Configuration file; missing files are created with documented defaults.
    #[arg(long, value_name = "PATH")]
    pub config: Option<PathBuf>,
    /// Atomically rebuild the current source's index using the configured bootstrap window.
    #[arg(long)]
    pub rebuild_index: bool,
}

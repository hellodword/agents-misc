use std::path::{Path, PathBuf};

use agents_viewer::{Result, ViewerError, model::typescript_contract};
use clap::{ArgGroup, Parser};

#[derive(Debug, Parser)]
#[command(about = "Generate or check the Agents Viewer TypeScript API contract")]
#[command(group(ArgGroup::new("mode").required(true).multiple(false)))]
struct Args {
    #[arg(long, group = "mode")]
    write: bool,
    #[arg(long, group = "mode")]
    check: bool,
}

fn main() -> Result<()> {
    let args = Args::parse();
    let output = output_path();
    if args.write {
        write_contract(&output)
    } else {
        check_contract(&output)
    }
}

fn output_path() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("web")
        .join("src")
        .join("generated")
        .join("api.ts")
}

fn write_contract(path: &Path) -> Result<()> {
    let parent = path
        .parent()
        .expect("generated contract path always has a parent");
    std::fs::create_dir_all(parent).map_err(|source| ViewerError::Io {
        path: parent.to_path_buf(),
        source,
    })?;
    std::fs::write(path, typescript_contract()).map_err(|source| ViewerError::Io {
        path: path.to_path_buf(),
        source,
    })
}

fn check_contract(path: &Path) -> Result<()> {
    let temp_dir =
        std::env::temp_dir().join(format!("agents-viewer-export-types-{}", std::process::id()));
    std::fs::create_dir_all(&temp_dir).map_err(|source| ViewerError::Io {
        path: temp_dir.clone(),
        source,
    })?;
    let temp_file = temp_dir.join("api.ts");
    std::fs::write(&temp_file, typescript_contract()).map_err(|source| ViewerError::Io {
        path: temp_file.clone(),
        source,
    })?;
    let expected = std::fs::read(path).map_err(|source| ViewerError::Io {
        path: path.to_path_buf(),
        source,
    })?;
    let actual = std::fs::read(&temp_file).map_err(|source| ViewerError::Io {
        path: temp_file.clone(),
        source,
    })?;
    let cleanup = std::fs::remove_dir_all(&temp_dir);
    if expected != actual {
        return Err(ViewerError::GeneratedContractOutOfDate(path.to_path_buf()));
    }
    cleanup.map_err(|source| ViewerError::Io {
        path: temp_dir,
        source,
    })
}

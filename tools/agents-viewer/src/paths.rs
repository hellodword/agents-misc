use std::ffi::OsString;
use std::path::{Path, PathBuf};

use sha2::{Digest, Sha256};

use crate::{Result, ViewerError};

#[derive(Clone, Debug)]
pub struct SourceRoots {
    pub home: PathBuf,
    pub active: Option<PathBuf>,
    pub archived: Option<PathBuf>,
}

#[derive(Clone, Debug)]
pub struct CachePaths {
    pub top: PathBuf,
    pub namespace: PathBuf,
    pub database: PathBuf,
    pub lock: PathBuf,
}

pub fn resolve_source_roots(codex_home: &Path) -> Result<SourceRoots> {
    let home = dunce::canonicalize(codex_home)
        .map_err(|_| ViewerError::SourceHomeUnavailable(codex_home.to_path_buf()))?;
    if !home.is_dir() {
        return Err(ViewerError::SourceHomeUnavailable(home));
    }
    let active = optional_source_root(&home.join("sessions"))?;
    let archived = optional_source_root(&home.join("archived_sessions"))?;
    Ok(SourceRoots {
        home,
        active,
        archived,
    })
}

pub fn resolve_cache_paths(source_home: &Path, data_dir: &Path) -> Result<CachePaths> {
    let top = canonicalize_allow_missing(data_dir)?;
    validate_no_overlap(source_home, &top)?;
    let namespace = top
        .join("sources")
        .join(&sha256_hex(source_home.as_os_str().as_encoded_bytes())[..16]);
    Ok(CachePaths {
        database: namespace.join("index.sqlite3"),
        lock: namespace.join("viewer.lock"),
        top,
        namespace,
    })
}

pub fn validate_no_overlap(source: &Path, cache: &Path) -> Result<()> {
    let source = canonicalize_allow_missing(source)?;
    let cache = canonicalize_allow_missing(cache)?;
    if source.starts_with(&cache) || cache.starts_with(&source) {
        return Err(ViewerError::PathOverlap {
            source_path: source,
            cache,
        });
    }
    Ok(())
}

pub fn canonicalize_allow_missing(path: &Path) -> Result<PathBuf> {
    let absolute = if path.is_absolute() {
        path.to_path_buf()
    } else {
        std::env::current_dir()
            .map_err(|source| ViewerError::Io {
                path: PathBuf::from("."),
                source,
            })?
            .join(path)
    };
    let mut ancestor = absolute.as_path();
    let mut missing = Vec::<OsString>::new();
    while !ancestor.exists() {
        let Some(name) = ancestor.file_name() else {
            return Err(ViewerError::InvalidArgument(format!(
                "path has no existing ancestor: {}",
                path.display()
            )));
        };
        missing.push(name.to_os_string());
        ancestor = ancestor.parent().ok_or_else(|| {
            ViewerError::InvalidArgument(format!(
                "path has no existing ancestor: {}",
                path.display()
            ))
        })?;
    }
    let mut resolved = dunce::canonicalize(ancestor).map_err(|source| ViewerError::Io {
        path: ancestor.to_path_buf(),
        source,
    })?;
    for component in missing.into_iter().rev() {
        resolved.push(component);
    }
    Ok(resolved)
}

fn optional_source_root(path: &Path) -> Result<Option<PathBuf>> {
    if !path.exists() {
        return Ok(None);
    }
    let root = dunce::canonicalize(path).map_err(|source| ViewerError::Io {
        path: path.to_path_buf(),
        source,
    })?;
    if !root.is_dir() {
        return Err(ViewerError::SourceHomeUnavailable(root));
    }
    Ok(Some(root))
}

fn sha256_hex(bytes: &[u8]) -> String {
    let digest = Sha256::digest(bytes);
    let mut output = String::with_capacity(digest.len() * 2);
    for byte in digest {
        use std::fmt::Write as _;
        write!(&mut output, "{byte:02x}").expect("writing to String cannot fail");
    }
    output
}

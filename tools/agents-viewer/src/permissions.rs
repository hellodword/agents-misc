use std::fs::{File, Metadata, OpenOptions};
use std::path::{Path, PathBuf};
use std::time::SystemTime;

use sha2::{Digest, Sha256};

use crate::{Result, ViewerError};

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct FileIdentity {
    pub file_key: String,
    pub size: u64,
    pub modified: Option<SystemTime>,
}

pub struct OpenedSource {
    pub file: File,
    pub canonical_path: PathBuf,
    pub identity: FileIdentity,
}

pub struct CacheLock {
    _file: File,
}

pub fn prepare_cache_directory(path: &Path) -> Result<()> {
    create_secure_dir_all(path)?;
    validate_cache_directory(path)
}

pub fn acquire_cache_lock(path: &Path) -> Result<CacheLock> {
    let parent = path.parent().ok_or_else(|| {
        ViewerError::InvalidArgument(format!("lock path has no parent: {}", path.display()))
    })?;
    prepare_cache_directory(parent)?;
    let file = open_secure_file(path, true)?;
    validate_cache_file(path)?;
    fs4::FileExt::try_lock(&file).map_err(|_| ViewerError::CacheLocked(path.to_path_buf()))?;
    Ok(CacheLock { _file: file })
}

pub fn open_secure_file(path: &Path, create: bool) -> Result<File> {
    let mut options = OpenOptions::new();
    options.read(true).write(true).create(create);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt as _;
        options.mode(0o600);
    }
    let file = options.open(path).map_err(|source| ViewerError::Io {
        path: path.to_path_buf(),
        source,
    })?;
    validate_cache_file(path)?;
    Ok(file)
}

pub fn open_source_read_only(root: &Path, path: &Path) -> Result<OpenedSource> {
    let symlink_metadata = std::fs::symlink_metadata(path).map_err(|source| ViewerError::Io {
        path: path.to_path_buf(),
        source,
    })?;
    if symlink_metadata.file_type().is_symlink() {
        return Err(ViewerError::SourceSymlink(path.to_path_buf()));
    }
    let canonical_path = dunce::canonicalize(path).map_err(|source| ViewerError::Io {
        path: path.to_path_buf(),
        source,
    })?;
    if !canonical_path.starts_with(root) {
        return Err(ViewerError::SourceOutsideRoot(canonical_path));
    }
    let before = file_identity(&symlink_metadata, &canonical_path);
    let file = File::open(&canonical_path).map_err(|source| ViewerError::Io {
        path: canonical_path.clone(),
        source,
    })?;
    let after_metadata = file.metadata().map_err(|source| ViewerError::Io {
        path: canonical_path.clone(),
        source,
    })?;
    let after = file_identity(&after_metadata, &canonical_path);
    if before != after {
        return Err(ViewerError::SourceChanged(canonical_path));
    }
    Ok(OpenedSource {
        file,
        canonical_path,
        identity: after,
    })
}

pub fn file_identity(metadata: &Metadata, canonical_path: &Path) -> FileIdentity {
    FileIdentity {
        file_key: platform_file_key(metadata).unwrap_or_else(|| {
            format!(
                "path:{}",
                sha256_hex(canonical_path.as_os_str().as_encoded_bytes())
            )
        }),
        size: metadata.len(),
        modified: metadata.modified().ok(),
    }
}

#[cfg(unix)]
fn create_secure_dir_all(path: &Path) -> Result<()> {
    use std::os::unix::fs::DirBuilderExt as _;
    if !path.exists() {
        let mut builder = std::fs::DirBuilder::new();
        builder.recursive(true).mode(0o700);
        builder.create(path).map_err(|source| ViewerError::Io {
            path: path.to_path_buf(),
            source,
        })?;
    }
    validate_cache_directory(path)
}

#[cfg(not(unix))]
fn create_secure_dir_all(path: &Path) -> Result<()> {
    std::fs::create_dir_all(path).map_err(|source| ViewerError::Io {
        path: path.to_path_buf(),
        source,
    })
}

#[cfg(unix)]
pub fn validate_cache_directory(path: &Path) -> Result<()> {
    use std::os::unix::fs::MetadataExt as _;
    let metadata = std::fs::metadata(path).map_err(|source| ViewerError::Io {
        path: path.to_path_buf(),
        source,
    })?;
    if !metadata.is_dir() {
        return Err(unsafe_permissions(path, "path is not a directory"));
    }
    if metadata.mode() & 0o077 != 0 {
        return Err(unsafe_permissions(
            path,
            "remove group/world permissions (expected mode 0700)",
        ));
    }
    if metadata.uid() != current_user_uid()? {
        return Err(unsafe_permissions(
            path,
            "directory is not owned by current user",
        ));
    }
    Ok(())
}

#[cfg(unix)]
pub fn validate_cache_file(path: &Path) -> Result<()> {
    use std::os::unix::fs::MetadataExt as _;
    let metadata = std::fs::metadata(path).map_err(|source| ViewerError::Io {
        path: path.to_path_buf(),
        source,
    })?;
    if !metadata.is_file() {
        return Err(unsafe_permissions(path, "path is not a regular file"));
    }
    if metadata.mode() & 0o077 != 0 {
        return Err(unsafe_permissions(
            path,
            "remove group/world permissions (expected mode 0600)",
        ));
    }
    if metadata.uid() != current_user_uid()? {
        return Err(unsafe_permissions(
            path,
            "file is not owned by current user",
        ));
    }
    Ok(())
}

#[cfg(unix)]
fn current_user_uid() -> Result<u32> {
    Ok(unsafe { libc::geteuid() })
}

#[cfg(windows)]
pub fn validate_cache_directory(path: &Path) -> Result<()> {
    windows_acl::validate(path, true)
}

#[cfg(windows)]
pub fn validate_cache_file(path: &Path) -> Result<()> {
    windows_acl::validate(path, false)
}

#[cfg(not(any(unix, windows)))]
pub fn validate_cache_directory(_path: &Path) -> Result<()> {
    Err(ViewerError::InvalidArgument(
        "cache permission checks are unsupported on this platform".into(),
    ))
}

#[cfg(not(any(unix, windows)))]
pub fn validate_cache_file(_path: &Path) -> Result<()> {
    Err(ViewerError::InvalidArgument(
        "cache permission checks are unsupported on this platform".into(),
    ))
}

#[cfg(unix)]
fn platform_file_key(metadata: &Metadata) -> Option<String> {
    use std::os::unix::fs::MetadataExt as _;
    Some(format!("unix:{}:{}", metadata.dev(), metadata.ino()))
}

#[cfg(windows)]
fn platform_file_key(metadata: &Metadata) -> Option<String> {
    use std::os::windows::fs::MetadataExt as _;
    Some(format!(
        "windows:{}:{}",
        metadata.volume_serial_number()?,
        metadata.file_index()?
    ))
}

#[cfg(not(any(unix, windows)))]
fn platform_file_key(_metadata: &Metadata) -> Option<String> {
    None
}

fn unsafe_permissions(path: &Path, reason: &str) -> ViewerError {
    ViewerError::UnsafeCachePermissions {
        path: path.to_path_buf(),
        reason: reason.into(),
    }
}

#[cfg(any(windows, test))]
const PRIVATE_CACHE_UNSAFE_RIGHTS: u32 = 0x0000_0001
    | 0x0000_0002
    | 0x0000_0004
    | 0x0000_0008
    | 0x0000_0010
    | 0x0000_0020
    | 0x0000_0040
    | 0x0000_0080
    | 0x0000_0100
    | 0x0001_0000
    | 0x0004_0000
    | 0x0008_0000
    | 0x1000_0000
    | 0x2000_0000
    | 0x4000_0000
    | 0x8000_0000;

#[cfg(any(windows, test))]
fn broad_principal_has_unsafe_rights(effective_rights: &[u32]) -> bool {
    effective_rights
        .iter()
        .any(|rights| rights & PRIVATE_CACHE_UNSAFE_RIGHTS != 0)
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

#[cfg(windows)]
mod windows_acl {
    use super::*;
    use std::os::windows::ffi::OsStrExt as _;
    use std::ptr::null_mut;

    use windows_sys::Win32::Foundation::LocalFree;
    use windows_sys::Win32::Security::Authorization::{
        BuildTrusteeWithSidW, ConvertStringSidToSidW, GetEffectiveRightsFromAclW,
        GetNamedSecurityInfoW, SE_FILE_OBJECT, TRUSTEE_W,
    };
    use windows_sys::Win32::Security::{
        ACL, DACL_SECURITY_INFORMATION, PSECURITY_DESCRIPTOR, PSID,
    };

    pub fn validate(path: &Path, directory: bool) -> Result<()> {
        let mut wide_path = path.as_os_str().encode_wide().collect::<Vec<_>>();
        wide_path.push(0);
        let mut dacl: *mut ACL = null_mut();
        let mut descriptor: PSECURITY_DESCRIPTOR = null_mut();
        // SAFETY: the path is NUL-terminated, output pointers are valid, and the returned
        // descriptor is released with LocalFree below.
        let status = unsafe {
            GetNamedSecurityInfoW(
                wide_path.as_ptr(),
                SE_FILE_OBJECT,
                DACL_SECURITY_INFORMATION,
                null_mut(),
                null_mut(),
                &mut dacl,
                null_mut(),
                &mut descriptor,
            )
        };
        if status != 0 {
            return Err(unsafe_permissions(
                path,
                &format!("GetNamedSecurityInfoW failed with Windows error {status}"),
            ));
        }
        if dacl.is_null() {
            // SAFETY: descriptor was allocated by GetNamedSecurityInfoW.
            unsafe { LocalFree(descriptor.cast()) };
            return Err(unsafe_permissions(
                path,
                "cache has an unrestricted null DACL",
            ));
        }

        let mut rights = Vec::with_capacity(3);
        for sid_text in ["S-1-1-0", "S-1-5-11", "S-1-5-32-545"] {
            match effective_rights_for_sid(dacl, sid_text) {
                Ok(value) => rights.push(value),
                Err(reason) => {
                    // SAFETY: descriptor was allocated by GetNamedSecurityInfoW.
                    unsafe { LocalFree(descriptor.cast()) };
                    return Err(unsafe_permissions(path, &reason));
                }
            }
        }
        // SAFETY: descriptor was allocated by GetNamedSecurityInfoW.
        unsafe { LocalFree(descriptor.cast()) };
        if broad_principal_has_unsafe_rights(&rights) {
            return Err(unsafe_permissions(
                path,
                if directory {
                    "World, Authenticated Users, or Builtin Users can access the cache directory"
                } else {
                    "World, Authenticated Users, or Builtin Users can access the cache file"
                },
            ));
        }
        Ok(())
    }

    fn effective_rights_for_sid(
        dacl: *const ACL,
        sid_text: &str,
    ) -> std::result::Result<u32, String> {
        let mut wide_sid = sid_text.encode_utf16().collect::<Vec<_>>();
        wide_sid.push(0);
        let mut sid: PSID = null_mut();
        // SAFETY: the SID text is NUL-terminated and the output pointer is valid.
        if unsafe { ConvertStringSidToSidW(wide_sid.as_ptr(), &mut sid) } == 0 {
            return Err(format!("ConvertStringSidToSidW failed for {sid_text}"));
        }
        let mut trustee = TRUSTEE_W::default();
        // SAFETY: sid is valid until LocalFree and trustee is a valid output structure.
        unsafe { BuildTrusteeWithSidW(&mut trustee, sid) };
        let mut rights = 0_u32;
        // SAFETY: dacl came from GetNamedSecurityInfoW, and trustee/rights remain valid.
        let status = unsafe { GetEffectiveRightsFromAclW(dacl, &trustee, &mut rights) };
        // SAFETY: sid was allocated by ConvertStringSidToSidW.
        unsafe { LocalFree(sid.cast()) };
        if status != 0 {
            return Err(format!(
                "GetEffectiveRightsFromAclW failed for {sid_text} with Windows error {status}"
            ));
        }
        Ok(rights)
    }
}

#[cfg(test)]
mod tests {
    use super::broad_principal_has_unsafe_rights;

    #[test]
    fn synthetic_broad_principal_rights_fail_closed() {
        assert!(broad_principal_has_unsafe_rights(&[0, 1, 0]));
        assert!(broad_principal_has_unsafe_rights(&[0x4000_0000]));
        assert!(!broad_principal_has_unsafe_rights(&[0, 0, 0]));
    }
}

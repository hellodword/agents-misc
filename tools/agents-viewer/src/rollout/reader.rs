use std::fs::File;
use std::io::{self, BufRead, BufReader, Read};
use std::path::Path;

use memchr::memchr;
use sha2::{Digest, Sha256};

use crate::error::{Result, ViewerError};
use crate::permissions::open_source_read_only;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum LineReadStatus {
    Complete,
    Oversize,
    IncompleteTail,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ReadLine {
    pub line_no: u64,
    pub byte_offset: u64,
    pub byte_length: u64,
    pub content_hash: String,
    pub status: LineReadStatus,
    pub bytes: Option<Vec<u8>>,
}

pub struct BoundedJsonlReader<R> {
    reader: R,
    max_event_bytes: usize,
    next_line_no: u64,
    next_offset: u64,
    finished: bool,
    stable_hasher: Sha256,
    stable_bytes: u64,
}

impl<R: BufRead> BoundedJsonlReader<R> {
    pub fn new(reader: R, max_event_bytes: usize) -> Self {
        Self {
            reader,
            max_event_bytes,
            next_line_no: 1,
            next_offset: 0,
            finished: false,
            stable_hasher: Sha256::new(),
            stable_bytes: 0,
        }
    }

    pub fn from_position(
        reader: R,
        max_event_bytes: usize,
        next_line_no: u64,
        next_offset: u64,
    ) -> Self {
        Self {
            reader,
            max_event_bytes,
            next_line_no,
            next_offset,
            finished: false,
            stable_hasher: Sha256::new(),
            stable_bytes: next_offset,
        }
    }

    pub fn stable_prefix(&self) -> FileCheckpoint {
        FileCheckpoint {
            offset: self.stable_bytes,
            prefix_hash: finish_hash(self.stable_hasher.clone()),
        }
    }

    pub fn read_next(&mut self) -> io::Result<Option<ReadLine>> {
        if self.finished {
            return Ok(None);
        }

        let line_no = self.next_line_no;
        let byte_offset = self.next_offset;
        let stable_hasher_before_line = self.stable_hasher.clone();
        let stable_bytes_before_line = self.stable_bytes;
        let mut bytes = Vec::with_capacity(self.max_event_bytes.min(8 * 1024));
        let mut hasher = Sha256::new();
        let mut byte_length = 0_u64;
        let mut oversize = false;

        loop {
            let buffer = self.reader.fill_buf()?;
            if buffer.is_empty() {
                self.finished = true;
                if byte_length == 0 {
                    return Ok(None);
                }
                self.stable_hasher = stable_hasher_before_line;
                self.stable_bytes = stable_bytes_before_line;
                self.next_offset = self.next_offset.saturating_add(byte_length);
                return Ok(Some(ReadLine {
                    line_no,
                    byte_offset,
                    byte_length,
                    content_hash: finish_hash(hasher),
                    status: LineReadStatus::IncompleteTail,
                    bytes: None,
                }));
            }

            let newline = memchr(b'\n', buffer);
            let take = newline.unwrap_or(buffer.len());
            let chunk = &buffer[..take];
            hasher.update(chunk);
            self.stable_hasher.update(chunk);
            byte_length = byte_length.saturating_add(chunk.len() as u64);

            if !oversize {
                let remaining = self.max_event_bytes.saturating_sub(bytes.len());
                if chunk.len() <= remaining {
                    bytes.extend_from_slice(chunk);
                } else {
                    bytes.clear();
                    oversize = true;
                }
            }

            let consumed = take + usize::from(newline.is_some());
            self.reader.consume(consumed);
            self.next_offset = self.next_offset.saturating_add(consumed as u64);

            if newline.is_some() {
                self.stable_hasher.update(b"\n");
                self.stable_bytes = self.next_offset;
                self.next_line_no = self.next_line_no.saturating_add(1);
                return Ok(Some(ReadLine {
                    line_no,
                    byte_offset,
                    byte_length,
                    content_hash: finish_hash(hasher),
                    status: if oversize {
                        LineReadStatus::Oversize
                    } else {
                        LineReadStatus::Complete
                    },
                    bytes: (!oversize).then_some(bytes),
                }));
            }
        }
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct FileCheckpoint {
    pub offset: u64,
    pub prefix_hash: String,
}

pub fn checkpoint_for_file(root: &Path, path: &Path, offset: u64) -> Result<FileCheckpoint> {
    let opened = open_source_read_only(root, path)?;
    Ok(FileCheckpoint {
        offset,
        prefix_hash: hash_prefix(opened.file, offset).map_err(|source| ViewerError::Io {
            path: path.to_path_buf(),
            source,
        })?,
    })
}

pub fn verify_checkpoint(root: &Path, path: &Path, checkpoint: &FileCheckpoint) -> Result<bool> {
    let opened = open_source_read_only(root, path)?;
    let length = opened
        .file
        .metadata()
        .map_err(|source| ViewerError::Io {
            path: path.to_path_buf(),
            source,
        })?
        .len();
    if length < checkpoint.offset {
        return Ok(false);
    }
    Ok(
        hash_prefix(opened.file, checkpoint.offset).map_err(|source| ViewerError::Io {
            path: path.to_path_buf(),
            source,
        })? == checkpoint.prefix_hash,
    )
}

fn hash_prefix(file: File, length: u64) -> io::Result<String> {
    let mut reader = BufReader::new(file).take(length);
    let mut hasher = Sha256::new();
    let mut buffer = [0_u8; 64 * 1024];
    let mut read_total = 0_u64;
    loop {
        let read = reader.read(&mut buffer)?;
        if read == 0 {
            break;
        }
        hasher.update(&buffer[..read]);
        read_total = read_total.saturating_add(read as u64);
    }
    if read_total != length {
        return Err(io::Error::new(
            io::ErrorKind::UnexpectedEof,
            "source ended before checkpoint",
        ));
    }
    Ok(finish_hash(hasher))
}

fn finish_hash(hasher: Sha256) -> String {
    let bytes = hasher.finalize();
    let mut output = String::with_capacity(bytes.len() * 2);
    for byte in bytes {
        use std::fmt::Write as _;
        write!(&mut output, "{byte:02x}").expect("writing to String cannot fail");
    }
    output
}

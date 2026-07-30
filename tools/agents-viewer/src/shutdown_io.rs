use std::future::Future;
use std::io;
use std::net::SocketAddr;
use std::pin::Pin;
use std::task::{Context, Poll};

use axum::serve::Listener;
use tokio::io::{AsyncRead, AsyncWrite, ReadBuf};
use tokio::net::{TcpListener, TcpStream};
use tokio_util::sync::CancellationToken;

/// Adds a connection-level escape hatch to Axum's otherwise unbounded graceful shutdown.
pub(super) struct ShutdownListener {
    inner: TcpListener,
    forced_shutdown: CancellationToken,
}

impl ShutdownListener {
    pub(super) fn new(inner: TcpListener, forced_shutdown: CancellationToken) -> Self {
        Self {
            inner,
            forced_shutdown,
        }
    }
}

impl Listener for ShutdownListener {
    type Io = ShutdownIo;
    type Addr = SocketAddr;

    async fn accept(&mut self) -> (Self::Io, Self::Addr) {
        let (stream, address) = Listener::accept(&mut self.inner).await;
        (
            ShutdownIo::new(stream, self.forced_shutdown.clone()),
            address,
        )
    }

    fn local_addr(&self) -> io::Result<Self::Addr> {
        self.inner.local_addr()
    }
}

pub(super) struct ShutdownIo {
    inner: TcpStream,
    forced_shutdown: Pin<Box<dyn Future<Output = ()> + Send>>,
    forced: bool,
}

impl ShutdownIo {
    fn new(inner: TcpStream, forced_shutdown: CancellationToken) -> Self {
        Self {
            inner,
            forced_shutdown: Box::pin(forced_shutdown.cancelled_owned()),
            forced: false,
        }
    }

    fn is_forced(&mut self, context: &mut Context<'_>) -> bool {
        if !self.forced && self.forced_shutdown.as_mut().poll(context).is_ready() {
            self.forced = true;
        }
        self.forced
    }
}

impl AsyncRead for ShutdownIo {
    fn poll_read(
        mut self: Pin<&mut Self>,
        context: &mut Context<'_>,
        buffer: &mut ReadBuf<'_>,
    ) -> Poll<io::Result<()>> {
        if self.is_forced(context) {
            return Poll::Ready(Ok(()));
        }
        Pin::new(&mut self.inner).poll_read(context, buffer)
    }
}

impl AsyncWrite for ShutdownIo {
    fn poll_write(
        mut self: Pin<&mut Self>,
        context: &mut Context<'_>,
        buffer: &[u8],
    ) -> Poll<Result<usize, io::Error>> {
        if self.is_forced(context) {
            return Poll::Ready(Err(forced_shutdown_error()));
        }
        Pin::new(&mut self.inner).poll_write(context, buffer)
    }

    fn poll_flush(
        mut self: Pin<&mut Self>,
        context: &mut Context<'_>,
    ) -> Poll<Result<(), io::Error>> {
        if self.is_forced(context) {
            return Poll::Ready(Err(forced_shutdown_error()));
        }
        Pin::new(&mut self.inner).poll_flush(context)
    }

    fn poll_shutdown(
        mut self: Pin<&mut Self>,
        context: &mut Context<'_>,
    ) -> Poll<Result<(), io::Error>> {
        if self.is_forced(context) {
            return Poll::Ready(Err(forced_shutdown_error()));
        }
        Pin::new(&mut self.inner).poll_shutdown(context)
    }

    fn is_write_vectored(&self) -> bool {
        self.inner.is_write_vectored()
    }

    fn poll_write_vectored(
        mut self: Pin<&mut Self>,
        context: &mut Context<'_>,
        buffers: &[io::IoSlice<'_>],
    ) -> Poll<Result<usize, io::Error>> {
        if self.is_forced(context) {
            return Poll::Ready(Err(forced_shutdown_error()));
        }
        Pin::new(&mut self.inner).poll_write_vectored(context, buffers)
    }
}

fn forced_shutdown_error() -> io::Error {
    io::Error::new(
        io::ErrorKind::ConnectionAborted,
        "HTTP connection closed during shutdown",
    )
}

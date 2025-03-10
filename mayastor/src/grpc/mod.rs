use std::{
    error::Error,
    fmt::{Debug, Display},
};

use futures::{channel::oneshot::Receiver, Future};
use tonic::{Response, Status};

pub use server::MayastorGrpcServer;

use crate::{
    core::{CoreError, Mthread, Reactor},
    nexus_uri::NexusBdevError,
    subsys::Config,
};

impl From<NexusBdevError> for tonic::Status {
    fn from(e: NexusBdevError) -> Self {
        match e {
            NexusBdevError::UrlParseError {
                ..
            } => Status::invalid_argument(e.to_string()),
            NexusBdevError::UriSchemeUnsupported {
                ..
            } => Status::invalid_argument(e.to_string()),
            NexusBdevError::UriInvalid {
                ..
            } => Status::invalid_argument(e.to_string()),
            e => Status::internal(e.to_string()),
        }
    }
}

impl From<CoreError> for tonic::Status {
    fn from(e: CoreError) -> Self {
        Status::internal(e.to_string())
    }
}
mod bdev_grpc;
mod json_grpc;
mod mayastor_grpc;
mod nexus_grpc;
mod server;

pub type GrpcResult<T> = std::result::Result<Response<T>, Status>;

/// call the given future within the context of the reactor on the first core
/// on the init thread, while the future is waiting to be completed the reactor
/// is continuously polled so that forward progress can be made
pub fn rpc_call<G, I, L, A>(future: G) -> Result<Response<A>, tonic::Status>
where
    G: Future<Output = Result<I, L>> + 'static,
    I: 'static,
    L: Into<Status> + Error + 'static,
    A: 'static + From<I>,
{
    Reactor::block_on(future)
        .unwrap()
        .map(|r| Response::new(A::from(r)))
        .map_err(|e| e.into())
}

pub fn rpc_submit<F, R, E>(
    future: F,
) -> Result<Receiver<Result<R, E>>, tonic::Status>
where
    E: Send + Debug + Display + 'static,
    F: Future<Output = Result<R, E>> + 'static,
    R: Send + Debug + 'static,
{
    Mthread::get_init()
        .spawn_local(future)
        .map_err(|_| Status::resource_exhausted("ENOMEM"))
}

/// Used by the gRPC method implementations to sync the current configuration by
/// exporting it to a config file
/// If `sync_config` fails then the method should return a failure
/// requiring the gRPC caller to retry the method, which should be idempotent
pub async fn sync_config<F, T>(future: F) -> GrpcResult<T>
where
    F: Future<Output = GrpcResult<T>>,
{
    let result = future.await;
    if result.is_ok() {
        if let Err(e) = Config::export_config() {
            error!("Failed to export config file: {}", e);
            return Err(Status::data_loss("Failed to export config"));
        }
    }
    result
}

macro_rules! default_ip {
    () => {
        "0.0.0.0"
    };
}
macro_rules! default_port {
    () => {
        10124
    };
}

/// Default server port
pub fn default_port() -> u16 {
    default_port!()
}

/// Default endpoint - ip:port
pub fn default_endpoint_str() -> &'static str {
    concat!(default_ip!(), ":", default_port!())
}

/// Default endpoint - ip:port
pub fn default_endpoint() -> std::net::SocketAddr {
    default_endpoint_str()
        .parse()
        .expect("Expected a valid endpoint")
}

/// If endpoint is missing a port number then add the default one.
pub fn endpoint(endpoint: String) -> std::net::SocketAddr {
    (if endpoint.contains(':') {
        endpoint
    } else {
        format!("{}:{}", endpoint, default_port())
    })
    .parse()
    .expect("Invalid gRPC endpoint")
}

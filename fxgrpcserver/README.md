# Fx gRPC Server Module

> Go [Fx](https://uber-go.github.io/fx/) module for the [grpcserver](https://github.com/ankorstore/yokai/tree/main/grpcserver) module

<!-- TOC -->
* [Installation](#installation)
* [Features](#features)
* [Documentation](#documentation)
  * [Dependencies](#dependencies)
  * [Loading](#loading)
  * [Configuration](#configuration)
  * [Registration](#registration)
  * [Reflection](#reflection)
  * [Healthcheck](#healthcheck)
  * [Decoration](#decoration)
  * [Testing](#testing)
<!-- TOC -->

## Installation

```shell
go get github.com/ankorstore/yokai/fxgrpcserver
```

## Features

This module provides the possibility to provide to your Fx application a gRPC server with:
- automatic panic recovery
- automatic reflection
- automatic logging and tracing (method, duration, status, ...)
- automatic metrics
- automatic healthcheck
- possibility to register gRPC services

## Documentation

### Dependencies

This module is intended to be used alongside:
- the [fxconfig](https://github.com/ankorstore/yokai/tree/main/fxconfig) module
- the [fxlog](https://github.com/ankorstore/yokai/tree/main/fxlog) module
- the [fxtrace](https://github.com/ankorstore/yokai/tree/main/fxtrace) module
- the [fxgenerate](https://github.com/ankorstore/yokai/tree/main/fxgenerate) module
- the [fxmetrics](https://github.com/ankorstore/yokai/tree/main/fxmetrics) module
- the [fxhealthcheck](https://github.com/ankorstore/yokai/tree/main/fxhealthcheck) module

### Loading

To load the module in your Fx application:

```go
package main

import (
	"github.com/ankorstore/yokai/fxconfig"
	"github.com/ankorstore/yokai/fxgenerate"
	"github.com/ankorstore/yokai/fxgrpcserver"
	"github.com/ankorstore/yokai/fxhealthcheck"
	"github.com/ankorstore/yokai/fxlog"
	"github.com/ankorstore/yokai/fxmetrics"
	"github.com/ankorstore/yokai/fxtrace"
	"go.uber.org/fx"
)

func main() {
	fx.New(
		fxconfig.FxConfigModule, // load the module dependencies
		fxlog.FxLogModule,
		fxtrace.FxTraceModule,
		fxgenerate.FxGenerateModule,
		fxmetrics.FxMetricsModule,
		fxhealthcheck.FxCheckerModule,
		fxgrpcserver.FxGrpcServerModule, // load the module
	).Run()
}
```

### Configuration

Configuration reference:

```yaml
# ./configs/config.yaml
app:
  name: app
  env: dev
  version: 0.1.0
  debug: true
modules:
  log:
    level: info
    output: stdout
  trace:
    processor:
      type: stdout
  grpc:
    server:
      port: 50051                   # 50051 by default
      log:
        metadata:                   # list of gRPC metadata to add to logs on top of x-request-id, empty by default
          x-foo: foo                # to log for example the metadata x-foo in the log field foo
          x-bar: bar
        exclude:                    # list of gRPC methods to exclude from logging, empty by default
          - /test.Service/Unary
      trace:
        enabled: true               # to trace gRPC calls, disabled by default
        exclude:                    # list of gRPC methods to exclude from tracing, empty by default
          - /test.Service/Bidi
      metrics:
        collect:
          enabled: true             # to collect gRPC server metrics, disabled by default
          namespace: app            # gRPC server metrics namespace (default app.name value)
          subsystem: grpcserver  # gRPC server metrics subsystem (default grpcserver)
        buckets: 0.1, 1, 10         # to override default request duration buckets (default prometheus.DefBuckets)
      reflection:
        enabled: true               # to expose gRPC reflection service, disabled by default
      healthcheck:
        enabled: true               # to expose gRPC healthcheck service, disabled by default
      test:
      	bufconn:
          size: 1048576             # test gRPC bufconn size, 1024*1024 by default
```

Notes:
- the gRPC calls logging will be based on the [fxlog](https://github.com/ankorstore/yokai/tree/main/fxlog) module configuration
- the gRPC calls tracing will be based on the [fxtrace](https://github.com/ankorstore/yokai/tree/main/fxtrace) module configuration
- if a request to an excluded gRPC method fails, the gRPC server will still log for observability purposes.

### Registration

This module offers the `fxgrpcserver.AsGrpcService()` function to easily register your gRPC services.

Example with the [TestService](testdata/service/service.go), server implementation for the [test.proto](testdata/proto/test.proto):

```go
package main

import (
	"github.com/ankorstore/yokai/fxconfig"
	"github.com/ankorstore/yokai/fxgenerate"
	"github.com/ankorstore/yokai/fxgrpcserver"
	"github.com/ankorstore/yokai/fxgrpcserver/testdata/proto"
	"github.com/ankorstore/yokai/fxgrpcserver/testdata/service"
	"github.com/ankorstore/yokai/fxhealthcheck"
	"github.com/ankorstore/yokai/fxlog"
	"github.com/ankorstore/yokai/fxmetrics"
	"github.com/ankorstore/yokai/fxtrace"
	"go.uber.org/fx"
)

func main() {
	fx.New(
		fxconfig.FxConfigModule, // load the module dependencies
		fxlog.FxLogModule,
		fxtrace.FxTraceModule,
		fxgenerate.FxGenerateModule,
		fxmetrics.FxMetricsModule,
		fxhealthcheck.FxCheckerModule,
		fxgrpcserver.FxGrpcServerModule, // load the module
		fx.Provide(
			fxgrpcserver.AsGrpcService(service.NewTestServiceServer, &proto.Service_ServiceDesc), // register the TestServiceServer for the proto.Service_ServiceDesc
		),
	).Run()
}
```

### Reflection

This module provides the possibility to enable [gRPC server reflection](https://github.com/grpc/grpc/blob/master/doc/server-reflection.md) if `modules.grpc.server.reflection.enabled=true`.

Reflection usage is helpful for developing or testing your gRPC services, but it is not recommended for production usage (disabled by default).

### Healthcheck

This module automatically expose the [GrpcHealthCheckService](https://github.com/ankorstore/yokai/blob/main/grpcserver/healthcheck.go) if `modules.grpc.server.healthcheck.enabled=true`, to offer the [Check and Watch](https://github.com/grpc/grpc-proto/blob/master/grpc/health/v1/health.proto) RPCs, suitable for [k8s gRPC startup, readiness or liveness probes](https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/).

You can use the `fxhealthcheck.AsCheckerProbe()` function to register several [CheckerProbe](https://github.com/ankorstore/yokai/blob/main/healthcheck/probe.go) (more details on the [fxhealthcheck](https://github.com/ankorstore/yokai/tree/main/fxhealthcheck) module documentation).

```go
package main

import (
	"context"

	"github.com/ankorstore/yokai/fxconfig"
	"github.com/ankorstore/yokai/fxgenerate"
	"github.com/ankorstore/yokai/fxgrpcserver"
	"github.com/ankorstore/yokai/fxhealthcheck"
	"github.com/ankorstore/yokai/fxlog"
	"github.com/ankorstore/yokai/fxmetrics"
	"github.com/ankorstore/yokai/fxtrace"
	"github.com/ankorstore/yokai/healthcheck"
	"go.uber.org/fx"
)

// success probe
type SuccessProbe struct{}

func NewSuccessProbe() *SuccessProbe {
	return &SuccessProbe{}
}

func (p *SuccessProbe) Name() string {
	return "successProbe"
}

func (p *SuccessProbe) Check(ctx context.Context) *healthcheck.CheckerProbeResult {
	return healthcheck.NewCheckerProbeResult(true, "success")
}

// failure probe
type FailureProbe struct{}

func NewFailureProbe() *FailureProbe {
	return &FailureProbe{}
}

func (p *FailureProbe) Name() string {
	return "failureProbe"
}

func (p *FailureProbe) Check(ctx context.Context) *healthcheck.CheckerProbeResult {
	return healthcheck.NewCheckerProbeResult(false, "failure")
}

// usage
func main() {
	fx.New(
		fxconfig.FxConfigModule, // load the module dependencies
		fxlog.FxLogModule,
		fxtrace.FxTraceModule,
		fxgenerate.FxGenerateModule,
		fxmetrics.FxMetricsModule,
		fxhealthcheck.FxCheckerModule,
		fxgrpcserver.FxGrpcServerModule,  // load the module
		fx.Provide(
			fxhealthcheck.AsCheckerProbe(NewSuccessProbe),                       // register the SuccessProbe probe for startup, liveness and readiness checks
			fxhealthcheck.AsCheckerProbe(NewFailureProbe, healthcheck.Liveness), // register the FailureProbe probe for liveness checks only
		),
	).Run()
}
```

In this example, the `GrpcHealthCheckService` will:
- run the liveness probes checks if the request service name contains liveness (like kubernetes::liveness) and will return a check failure
- or run the readiness probes checks if the request service name contains readiness (like kubernetes::readiness) and will return a check success
- or run the startup probes checks otherwise, and will return a check success

### Decoration

By default, the `grpc.Server` is created by the [DefaultGrpcServerFactory](https://github.com/ankorstore/yokai/blob/main/grpcserver/factory.go).

If needed, you can provide your own factory and decorate the module:

```go
package main

import (
	"github.com/ankorstore/yokai/fxconfig"
	"github.com/ankorstore/yokai/fxgenerate"
	"github.com/ankorstore/yokai/fxgrpcserver"
	"github.com/ankorstore/yokai/fxhealthcheck"
	"github.com/ankorstore/yokai/fxlog"
	"github.com/ankorstore/yokai/fxmetrics"
	"github.com/ankorstore/yokai/fxtrace"
	"github.com/ankorstore/yokai/grpcserver"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

type CustomGrpcServerFactory struct{}

func NewCustomGrpcServerFactory() grpcserver.GrpcServerFactory {
	return &CustomGrpcServerFactory{}
}

func (f *CustomGrpcServerFactory) Create(options ...grpcserver.GrpcServerOption) (*grpc.Server, error) {
	return grpc.NewServer(...), nil
}

func main() {
	fx.New(
		fxconfig.FxConfigModule, // load the module dependencies
		fxlog.FxLogModule,
		fxtrace.FxTraceModule,
		fxgenerate.FxGenerateModule,
		fxmetrics.FxMetricsModule,
		fxhealthcheck.FxCheckerModule,
		fxgrpcserver.FxGrpcServerModule,         // load the module
		fx.Decorate(NewCustomGrpcServerFactory), // decorate the module with a custom factory
		fx.Invoke(func(grpcServer *grpc.Server) { // invoke the gRPC server
			// ...
		}),
	).Run()
}
```

### Testing

This module provides a `*bufconn.Listener` that will automatically be used by the gRPC server in `test` mode.

You can then use this listener on your gRPC clients to provide `functional` tests for your gRPC services.

You can find tests examples in this [module own tests](module_test.go).

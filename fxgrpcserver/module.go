package fxgrpcserver

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/ankorstore/yokai/config"
	"github.com/ankorstore/yokai/generate/uuid"
	"github.com/ankorstore/yokai/grpcserver"
	"github.com/ankorstore/yokai/grpcserver/grpcservertest"
	"github.com/ankorstore/yokai/healthcheck"
	"github.com/ankorstore/yokai/log"
	grpcprom "github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc/filters"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/test/bufconn"
)

const (
	ModuleName         = "grpcserver"
	DefaultPort        = 50051
	DefaultBufconnSize = 1024 * 1024
)

var FxGrpcServerModule = fx.Module(
	ModuleName,
	fx.Provide(
		grpcserver.NewDefaultGrpcServerFactory,
		NewFxGrpcBufconnListener,
		NewFxGrpcServerRegistry,
		NewFxGrpcServer,
		fx.Annotate(
			NewFxGrpcServerModuleInfo,
			fx.As(new(interface{})),
			fx.ResultTags(`group:"core-module-infos"`),
		),
	),
)

type FxGrpcBufconnListenerParam struct {
	fx.In
	Config *config.Config
}

func NewFxGrpcBufconnListener(p FxGrpcBufconnListenerParam) *bufconn.Listener {
	size := p.Config.GetInt("modules.grpc.server.test.bufconn.size")
	if size == 0 {
		size = DefaultBufconnSize
	}

	return grpcservertest.NewBufconnListener(size)
}

type FxGrpcServerParam struct {
	fx.In
	LifeCycle       fx.Lifecycle
	Factory         grpcserver.GrpcServerFactory
	Generator       uuid.UuidGenerator
	Listener        *bufconn.Listener
	Registry        *GrpcServerRegistry
	Config          *config.Config
	Logger          *log.Logger
	Checker         *healthcheck.Checker
	TracerProvider  trace.TracerProvider
	MetricsRegistry *prometheus.Registry
}

func NewFxGrpcServer(p FxGrpcServerParam) (*grpc.Server, error) {
	// server interceptors
	unaryInterceptors, streamInterceptors := createInterceptors(p)

	// server options
	grpcServerOptions := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(unaryInterceptors...),
		grpc.ChainStreamInterceptor(streamInterceptors...),
	}

	grpcServerOptions = append(grpcServerOptions, p.Registry.ResolveGrpcServerOptions()...)

	// server
	grpcServer, err := p.Factory.Create(
		grpcserver.WithServerOptions(grpcServerOptions...),
		grpcserver.WithReflection(p.Config.GetBool("modules.grpc.server.reflection.enabled")),
	)
	if err != nil {
		return nil, err
	}

	// healthcheck
	if p.Config.GetBool("modules.grpc.server.healthcheck.enabled") {
		grpcServer.RegisterService(&grpc_health_v1.Health_ServiceDesc, grpcserver.NewGrpcHealthCheckService(p.Checker))
	}

	// registrations
	resolvedServices, err := p.Registry.ResolveGrpcServerServices()
	if err != nil {
		return nil, err
	}

	for _, service := range resolvedServices {
		grpcServer.RegisterService(service.Description(), service.Implementation())
	}

	// lifecycles
	p.LifeCycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			port := p.Config.GetInt("modules.grpc.server.port")
			if port == 0 {
				port = DefaultPort
			}

			go func() {
				var lis net.Listener
				if p.Config.IsTestEnv() {
					lis = p.Listener
				} else {
					lis, err = net.Listen("tcp", fmt.Sprintf(":%d", port))
					if err != nil {
						p.Logger.Error().Err(err).Msgf("failed to listen on %d for grpc server", port)
					}
				}

				if err = grpcServer.Serve(lis); err != nil {
					p.Logger.Error().Err(err).Msg("failed to serve grpc server")
				}
			}()

			return nil
		},
		OnStop: func(ctx context.Context) error {
			if !p.Config.IsTestEnv() {
				grpcServer.GracefulStop()
			}

			return nil
		},
	})

	return grpcServer, nil
}

//nolint:cyclop
func createInterceptors(p FxGrpcServerParam) ([]grpc.UnaryServerInterceptor, []grpc.StreamServerInterceptor) {
	// panic recovery
	panicRecoveryHandler := grpcserver.NewGrpcPanicRecoveryHandler()

	// interceptors
	unaryInterceptors := []grpc.UnaryServerInterceptor{
		recovery.UnaryServerInterceptor(
			recovery.WithRecoveryHandlerContext(panicRecoveryHandler.Handle(p.Config.AppDebug())),
		),
	}

	streamInterceptors := []grpc.StreamServerInterceptor{
		recovery.StreamServerInterceptor(
			recovery.WithRecoveryHandlerContext(panicRecoveryHandler.Handle(p.Config.AppDebug())),
		),
	}

	// tracer
	if p.Config.GetBool("modules.grpc.server.trace.enabled") {
		var methodFilters []otelgrpc.Filter
		for _, method := range p.Config.GetStringSlice("modules.grpc.server.trace.exclude") {
			methodFilters = append(methodFilters, filters.FullMethodName(method))
		}

		unaryInterceptors = append(
			unaryInterceptors,
			otelgrpc.UnaryServerInterceptor(
				otelgrpc.WithTracerProvider(p.TracerProvider),
				otelgrpc.WithInterceptorFilter(filters.None(methodFilters...)),
			),
		)
		streamInterceptors = append(
			streamInterceptors,
			otelgrpc.StreamServerInterceptor(
				otelgrpc.WithTracerProvider(p.TracerProvider),
				otelgrpc.WithInterceptorFilter(filters.None(methodFilters...)),
			),
		)
	}

	// logger
	loggerInterceptor := grpcserver.
		NewGrpcLoggerInterceptor(p.Generator, log.FromZerolog(p.Logger.ToZerolog().With().Str("system", ModuleName).Logger())).
		Metadata(p.Config.GetStringMapString("modules.grpc.server.log.metadata")).
		Exclude(p.Config.GetStringSlice("modules.grpc.server.log.exclude")...)

	unaryInterceptors = append(unaryInterceptors, loggerInterceptor.UnaryInterceptor())
	streamInterceptors = append(streamInterceptors, loggerInterceptor.StreamInterceptor())

	// metrics
	if p.Config.GetBool("modules.grpc.server.metrics.collect.enabled") {
		namespace := p.Config.GetString("modules.grpc.server.metrics.collect.namespace")
		if namespace == "" {
			namespace = p.Config.AppName()
		}

		subsystem := p.Config.GetString("modules.grpc.server.metrics.collect.subsystem")
		if subsystem == "" {
			subsystem = ModuleName
		}

		grpcSrvMetricsSubsystem := strings.ReplaceAll(fmt.Sprintf("%s_%s", namespace, subsystem), "-", "_")

		var grpcSrvMetricsBuckets []float64
		if bucketsConfig := p.Config.GetString("modules.grpc.server.metrics.buckets"); bucketsConfig != "" {
			for _, s := range strings.Split(strings.ReplaceAll(bucketsConfig, " ", ""), ",") {
				f, err := strconv.ParseFloat(s, 64)
				if err == nil {
					grpcSrvMetricsBuckets = append(grpcSrvMetricsBuckets, f)
				}
			}
		}

		if len(grpcSrvMetricsBuckets) == 0 {
			grpcSrvMetricsBuckets = prometheus.DefBuckets
		}

		grpcSrvMetrics := grpcprom.NewServerMetrics(
			grpcprom.WithServerCounterOptions(
				grpcprom.WithSubsystem(grpcSrvMetricsSubsystem),
			),
			grpcprom.WithServerHandlingTimeHistogram(
				grpcprom.WithHistogramSubsystem(grpcSrvMetricsSubsystem),
				grpcprom.WithHistogramBuckets(grpcSrvMetricsBuckets),
			),
		)

		p.MetricsRegistry.MustRegister(grpcSrvMetrics)

		exemplar := func(ctx context.Context) prometheus.Labels {
			if span := trace.SpanContextFromContext(ctx); span.IsSampled() {
				return prometheus.Labels{
					"traceID": span.TraceID().String(),
					"spanID":  span.SpanID().String(),
				}
			}

			return nil
		}

		unaryInterceptors = append(
			unaryInterceptors,
			grpcSrvMetrics.UnaryServerInterceptor(grpcprom.WithExemplarFromContext(exemplar)),
		)

		streamInterceptors = append(
			streamInterceptors,
			grpcSrvMetrics.StreamServerInterceptor(grpcprom.WithExemplarFromContext(exemplar)),
		)
	}

	return unaryInterceptors, streamInterceptors
}

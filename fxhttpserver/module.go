package fxhttpserver

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/ankorstore/yokai/config"
	"github.com/ankorstore/yokai/generate/uuid"
	"github.com/ankorstore/yokai/httpserver"
	httpservermiddleware "github.com/ankorstore/yokai/httpserver/middleware"
	"github.com/ankorstore/yokai/log"
	"github.com/labstack/echo/v4"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/fx"
)

const (
	ModuleName  = "httpserver"
	DefaultPort = 8080
)

// FxHttpServerModule is the [Fx] httpserver module.
//
// [Fx]: https://github.com/uber-go/fx
var FxHttpServerModule = fx.Module(
	ModuleName,
	fx.Provide(
		httpserver.NewDefaultHttpServerFactory,
		NewFxHttpServerRegistry,
		NewFxHttpServer,
		fx.Annotate(
			NewFxHttpServerModuleInfo,
			fx.As(new(interface{})),
			fx.ResultTags(`group:"core-module-infos"`),
		),
	),
)

// FxHttpServerParam allows injection of the required dependencies in [NewFxHttpServer].
type FxHttpServerParam struct {
	fx.In
	LifeCycle       fx.Lifecycle
	Factory         httpserver.HttpServerFactory
	Generator       uuid.UuidGenerator
	Registry        *HttpServerRegistry
	Config          *config.Config
	Logger          *log.Logger
	TracerProvider  trace.TracerProvider
	MetricsRegistry *prometheus.Registry
}

// NewFxHttpServer returns a new [echo.Echo].
func NewFxHttpServer(p FxHttpServerParam) (*echo.Echo, error) {
	appDebug := p.Config.AppDebug()

	// logger
	echoLogger := httpserver.NewEchoLogger(
		log.FromZerolog(p.Logger.ToZerolog().With().Str("module", ModuleName).Logger()),
	)

	// renderer
	var renderer echo.Renderer
	if p.Config.GetBool("modules.http.server.templates.enabled") {
		renderer = httpserver.NewHtmlTemplateRenderer(p.Config.GetString("modules.http.server.templates.path"))
	}

	// server
	httpServer, err := p.Factory.Create(
		httpserver.WithDebug(appDebug),
		httpserver.WithBanner(false),
		httpserver.WithRecovery(true),
		httpserver.WithLogger(echoLogger),
		httpserver.WithRenderer(renderer),
		httpserver.WithHttpErrorHandler(
			httpserver.JsonErrorHandler(
				p.Config.GetBool("modules.http.server.errors.obfuscate") || !appDebug,
				p.Config.GetBool("modules.http.server.errors.stack") || appDebug,
			),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create http server: %w", err)
	}

	// middlewares
	httpServer = withDefaultMiddlewares(httpServer, p)

	// groups, handlers & middlewares registrations
	httpServer = withRegisteredResources(httpServer, p)

	// lifecycles
	p.LifeCycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if !p.Config.IsTestEnv() {
				port := p.Config.GetInt("modules.http.server.port")
				if port == 0 {
					port = DefaultPort
				}

				//nolint:errcheck
				go httpServer.Start(fmt.Sprintf(":%d", port))
			}

			return nil
		},
		OnStop: func(ctx context.Context) error {
			if !p.Config.IsTestEnv() {
				return httpServer.Shutdown(ctx)
			}

			return nil
		},
	})

	return httpServer, nil
}

func withDefaultMiddlewares(httpServer *echo.Echo, p FxHttpServerParam) *echo.Echo {
	// request id middleware
	httpServer.Use(httpservermiddleware.RequestIdMiddlewareWithConfig(
		httpservermiddleware.RequestIdMiddlewareConfig{
			Generator: p.Generator,
		},
	))

	// request tracer middleware
	if p.Config.GetBool("modules.http.server.trace.enabled") {
		httpServer.Use(httpservermiddleware.RequestTracerMiddlewareWithConfig(
			p.Config.AppName(),
			httpservermiddleware.RequestTracerMiddlewareConfig{
				TracerProvider:              p.TracerProvider,
				RequestUriPrefixesToExclude: p.Config.GetStringSlice("modules.http.server.trace.exclude"),
			},
		))
	}

	// request logger middleware
	requestHeadersToLog := map[string]string{
		httpservermiddleware.HeaderXRequestId: httpservermiddleware.LogFieldRequestId,
	}

	for headerName, fieldName := range p.Config.GetStringMapString("modules.http.server.log.headers") {
		requestHeadersToLog[headerName] = fieldName
	}

	httpServer.Use(httpservermiddleware.RequestLoggerMiddlewareWithConfig(
		httpservermiddleware.RequestLoggerMiddlewareConfig{
			RequestHeadersToLog:             requestHeadersToLog,
			RequestUriPrefixesToExclude:     p.Config.GetStringSlice("modules.http.server.log.exclude"),
			LogLevelFromResponseOrErrorCode: p.Config.GetBool("modules.http.server.log.level_from_response"),
		},
	))

	// request metrics middleware
	if p.Config.GetBool("modules.http.server.metrics.collect.enabled") {
		namespace := p.Config.GetString("modules.http.server.metrics.collect.namespace")
		if namespace == "" {
			namespace = p.Config.AppName()
		}

		subsystem := p.Config.GetString("modules.http.server.metrics.collect.subsystem")
		if subsystem == "" {
			subsystem = ModuleName
		}

		var buckets []float64
		if bucketsConfig := p.Config.GetString("modules.http.server.metrics.buckets"); bucketsConfig != "" {
			for _, s := range strings.Split(strings.ReplaceAll(bucketsConfig, " ", ""), ",") {
				f, err := strconv.ParseFloat(s, 64)
				if err == nil {
					buckets = append(buckets, f)
				}
			}
		}

		metricsMiddlewareConfig := httpservermiddleware.RequestMetricsMiddlewareConfig{
			Registry:            p.MetricsRegistry,
			Namespace:           strings.ReplaceAll(namespace, "-", "_"),
			Subsystem:           strings.ReplaceAll(subsystem, "-", "_"),
			Buckets:             buckets,
			NormalizeHTTPStatus: p.Config.GetBool("modules.http.server.metrics.normalize"),
		}

		httpServer.Use(httpservermiddleware.RequestMetricsMiddlewareWithConfig(metricsMiddlewareConfig))
	}

	return httpServer
}

func withRegisteredResources(httpServer *echo.Echo, p FxHttpServerParam) *echo.Echo {
	// register handler groups
	resolvedHandlersGroups, err := p.Registry.ResolveHandlersGroups()
	if err != nil {
		httpServer.Logger.Errorf("cannot resolve router handlers groups: %v", err)
	}

	for _, g := range resolvedHandlersGroups {
		group := httpServer.Group(g.Prefix(), g.Middlewares()...)

		for _, h := range g.Handlers() {
			group.Add(
				strings.ToUpper(h.Method()),
				h.Path(),
				h.Handler(),
				h.Middlewares()...,
			)
			httpServer.Logger.Debugf("registering handler in group for [%s]%s%s", h.Method(), g.Prefix(), h.Path())
		}

		httpServer.Logger.Debugf("registered handlers group for prefix %s", g.Prefix())
	}

	// register middlewares
	resolvedMiddlewares, err := p.Registry.ResolveMiddlewares()
	if err != nil {
		httpServer.Logger.Errorf("cannot resolve router middlewares: %v", err)
	}

	for _, m := range resolvedMiddlewares {
		if m.Kind() == GlobalPre {
			httpServer.Pre(m.Middleware())
		}

		if m.Kind() == GlobalUse {
			httpServer.Use(m.Middleware())
		}

		httpServer.Logger.Debugf("registered %s middleware %T", m.Kind().String(), m.Middleware())
	}

	// register handlers
	resolvedHandlers, err := p.Registry.ResolveHandlers()
	if err != nil {
		httpServer.Logger.Errorf("cannot resolve router handlers: %v", err)
	}

	for _, h := range resolvedHandlers {
		httpServer.Add(
			strings.ToUpper(h.Method()),
			h.Path(),
			h.Handler(),
			h.Middlewares()...,
		)

		httpServer.Logger.Debugf("registered handler for [%s]%s", h.Method(), h.Path())
	}

	return httpServer
}

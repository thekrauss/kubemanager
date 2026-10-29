package router

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/thekrauss/kubemanager/internal/iam"
	"github.com/thekrauss/kubemanager/internal/iam/sts"
	"github.com/thekrauss/kubemanager/internal/infrastructure/objectstore"
	sitesRepo "github.com/thekrauss/kubemanager/internal/modules/sites/repository"

	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
	"github.com/thekrauss/beto-shared/pkg/tracing"
	k8sprovider "github.com/thekrauss/kubemanager/internal/infrastructure/kubernetes"
	"go.temporal.io/sdk/client"

	"github.com/thekrauss/kubemanager/internal/core/cache"
	"github.com/thekrauss/kubemanager/internal/core/workerpool"
	"github.com/thekrauss/kubemanager/internal/infrastructure/database"
	idempotency "github.com/thekrauss/kubemanager/internal/infrastructure/idempotence"
	"github.com/thekrauss/kubemanager/internal/infrastructure/observability"
	"github.com/thekrauss/kubemanager/internal/infrastructure/realtime"
	"github.com/thekrauss/kubemanager/internal/infrastructure/temporal"
	"github.com/thekrauss/kubemanager/internal/middleware"
	"github.com/thekrauss/kubemanager/internal/middleware/security"
	"github.com/thekrauss/kubemanager/internal/modules/auth/repository"
	authSvc "github.com/thekrauss/kubemanager/internal/modules/auth/service"
	identityKC "github.com/thekrauss/kubemanager/internal/modules/identity/keycloak"
	projectRepos "github.com/thekrauss/kubemanager/internal/modules/projects/repository"
	workloadsRepo "github.com/thekrauss/kubemanager/internal/modules/workloads/repository"
)

func (a *App) initDependencies() error {
	ctx := context.Background()
	a.Logger.Infow("Initializing application dependencies", "service", a.Config.ServiceName)

	if err := a.initTracing(ctx); err != nil {
		return err
	}

	if err := a.initDatabase(); err != nil {
		return err
	}

	if err := a.initCache(ctx); err != nil {
		return err
	}

	if err := a.initAuthCache(); err != nil {
		return err
	}

	if err := a.initAsyncPool(); err != nil {
		return err
	}

	if err := a.initRealtime(); err != nil {
		return err
	}

	if err := a.initIdempotency(); err != nil {
		return err
	}

	if err := a.initKeycloak(); err != nil {
		return err
	}

	if err := a.initTemporalClient(); err != nil {
		return err
	}

	if err := a.initKubernetes(); err != nil {
		return err

	}

	if err := a.initObjectStore(ctx); err != nil {
		return err
	}

	a.initRepositories()

	if err := a.initUsageAccumulator(ctx); err != nil {
		return err
	}

	if err := a.initSecurity(); err != nil {
		return err
	}

	if err := a.initDomain(ctx); err != nil {
		return err
	}

	return nil
}

func (a *App) initAuthCache() error {
	authCache, err := cache.NewRistrettoAuthCache()
	if err != nil {
		return fmt.Errorf("ristretto auth cache init: %w", err)
	}
	a.AuthCache = authCache
	return nil
}

func (a *App) initAsyncPool() error {
	a.AsyncPool = workerpool.New(workerpool.Config{
		Workers:     16,
		QueueSize:   1024,
		TaskTimeout: 5 * time.Second,
		Logger:      a.Logger,
	})
	return nil
}

func (a *App) initUsageAccumulator(ctx context.Context) error {
	a.UsageAccumulator = authSvc.NewUsageAccumulator(authSvc.AccumulatorConfig{
		Repo:          a.Repos.Auth,
		Logger:        a.Logger,
		FlushInterval: 5 * time.Second,
		MaxPending:    500,
	})
	a.UsageAccumulator.Start(ctx)
	return nil
}

func (a *App) initRealtime() error {
	pub, err := realtime.NewPublisher(a.Config.Centrifugo)
	if err != nil {
		return fmt.Errorf("realtime publisher init: %w", err)
	}
	a.Publisher = pub

	signer, err := realtime.NewTokenSigner(a.Config.Centrifugo)
	if err != nil {
		return fmt.Errorf("realtime token signer init: %w", err)
	}
	a.RealtimeSigner = signer

	if a.Config.Centrifugo.Enabled {
		a.Logger.Infow("realtime ready", "api_url", a.Config.Centrifugo.APIURL, "ws_url", a.Config.Centrifugo.URL)
	} else {
		a.Logger.Infow("realtime disabled (noop publisher + signer)")
	}
	return nil
}

func (a *App) initIdempotency() error {
	opt := &redis.Options{
		Addr:     fmt.Sprintf("%s:%d", a.Config.Redis.Host, a.Config.Redis.Port),
		Password: a.Config.Redis.Password,
		DB:       a.Config.Redis.DB,
	}
	mgr := idempotency.NewRedisManager(opt, 12*time.Hour)
	a.Idempotency = mgr
	middleware.SetIdempotencyManager(mgr, a.Logger)
	return nil
}

func (a *App) initKeycloak() error {
	cfg := a.Config.Keycloak
	if !cfg.Enabled {
		a.Logger.Warnw("keycloak disabled — identity module will be nil")
		return nil
	}
	client, err := identityKC.New(identityKC.AdminConfig{
		BaseURL:      cfg.AdminBaseURL,
		Realm:        cfg.AdminRealm,
		ClientID:     cfg.AdminClientID,
		ClientSecret: cfg.AdminClientSecret,
		WebClientID:  cfg.WebClientID,
		Timeout:      cfg.HTTPTimeout,
	}, cfg.IssuerURL)
	if err != nil {
		return fmt.Errorf("keycloak client init: %w", err)
	}
	a.Keycloak = client
	a.Logger.Infow("keycloak client ready", "issuer", cfg.IssuerURL, "realm", cfg.Realm)
	return nil
}

func (a *App) initTracing(ctx context.Context) error {
	if a.Config.Tracing.JaegerEndpoint == "" {
		a.Observability.TracerShutdown = func() {}
		return nil
	}

	sampleRatio := a.Config.Tracing.SampleRatio
	if sampleRatio <= 0 {
		sampleRatio = 0.01
	}
	if sampleRatio > 1 {
		sampleRatio = 1
	}
	_, shutdown, err := tracing.InitTracerProvider(
		ctx,
		a.Config.ServiceName,
		a.Config.Tracing.JaegerEndpoint,
		sampleRatio,
	)
	if err != nil {
		a.Logger.Warnw("Tracing initialization failed", "error", err)
		a.Observability.TracerShutdown = func() {}
		return nil
	}

	a.Observability.TracerShutdown = shutdown
	return nil
}

func (a *App) initDatabase() error {
	a.Logger.Info("initializing Database Provider...")
	provider, err := database.NewDBProvider(a.Config.Database, a.Config.Logger.Level)
	if err != nil {
		return err
	}
	a.DB = provider.DB
	a.Logger.Info("SQL migrations applied")
	return nil
}

func (a *App) initCache(ctx context.Context) error {
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", a.Config.Redis.Host, a.Config.Redis.Port),
		Password: a.Config.Redis.Password,
		DB:       a.Config.Redis.DB,
	})

	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis connection failed: %w", err)
	}

	if err := redisotel.InstrumentTracing(rdb); err != nil {
		a.Logger.Warnw("redisotel tracing init failed — continuing without", "error", err)
	}
	if err := redisotel.InstrumentMetrics(rdb); err != nil {
		a.Logger.Warnw("redisotel metrics init failed — continuing without", "error", err)
	}

	a.Redis = rdb
	a.Cache = cache.NewcacheRedis(rdb, a.Logger)
	return nil
}

func (a *App) initSecurity() error {
	jwksURL := strings.TrimSpace(a.Config.Keycloak.JWKSURL)
	if jwksURL == "" {
		jwksURL = strings.TrimSpace(a.Config.Keycloak.IssuerURL + "/protocol/openid-connect/certs")
	}
	audience := a.Config.Keycloak.Audience
	if audience == "" {
		audience = a.Config.Keycloak.ClientID
	}
	verifier, err := middleware.NewVerifier(middleware.Config{
		JWKSURL:  jwksURL,
		Issuer:   a.Config.Keycloak.IssuerURL,
		Audience: audience,
	}, a.Logger)
	if err != nil {
		return fmt.Errorf("failed to init JWKS verifier: %w", err)
	}
	a.Security.Verifier = verifier

	audit := middleware.NewAuditLoggerWithSampling(a.Logger.Desugar(), a.Config.Audit.SampleRate)
	a.Security.Audit = audit

	tenantResolver := middleware.NewTenantResolver(a.Config)
	a.Security.TenantResolver = tenantResolver

	a.Security.Middleware = security.NewMiddlewareManager(
		a.Config,
		verifier,
		a.Logger,
		a.Repos.Auth,
		audit,
		a.AuthCache,
		tenantResolver,
		a.AsyncPool,
		a.UsageAccumulator,
	)

	// IAM : point de décision unique des autorisations (docs/architecture/iam.md).
	a.IAM = iam.NewEngine(&iam.GormStore{DB: a.DB}, iam.DefaultCacheTTL)
	a.Security.Middleware.SetAuthorizer(a.IAM)
	a.startIAMSync(context.Background())

	sa, err := iam.NewServiceAccountService(a.DB, a.IAM, []byte(a.Config.IAM.APIKeyPepper))
	if err != nil {
		return fmt.Errorf("iam: %w (set iam.api_key_pepper / KUBEMANAGER_IAM_API_KEY_PEPPER)", err)
	}
	a.ServiceAccounts = sa
	a.Security.Middleware.SetKeyAuthenticator(sa)
	a.ClusterAgents = iam.NewClusterAgents(sa)
	if a.AuthCache != nil {
		authCache := a.AuthCache
		a.ClusterAgents.RevokeLegacyPrefix = func(ctx context.Context, prefix string) {
			_ = authCache.RevokeAPIKeyPrefix(ctx, prefix, 30*24*time.Hour)
		}
	}
	a.Security.Middleware.SetClusterAgents(a.ClusterAgents)

	// Double authentification des personnes (iam.md §9) : step-up sur les
	// actions sensibles, iam.requireMfa par organisation.
	a.OrgPolicies = iam.NewOrgPolicyService(a.DB, a.IAM, len(a.Config.IAM.MFAACRValues) > 0)
	a.Security.Middleware.SetMFA(security.MFAConfig{ACRValues: a.Config.IAM.MFAACRValues,
		MaxAge: a.stepUpMaxAge(), Policy: a.OrgPolicies})
	if len(a.Config.IAM.MFAACRValues) == 0 {
		a.Logger.Warnw("iam: mfa_acr_values is empty — step-up and organization MFA requirement are disabled")
	}

	// STS : jetons machine (iam.md §7.3). Obligatoire, comme le pepper.
	if err := a.initSTS(); err != nil {
		return err
	}
	a.startPlatformRolesReconciler(context.Background())
	return nil
}

// initObjectStore : stockage des sites statiques. Le bucket est créé s'il
// manque ; un échec de création n'empêche pas le démarrage (bucket peut-être
// déjà provisionné par l'ops, ou object store momentanément indisponible).
func (a *App) initObjectStore(ctx context.Context) error {
	if !a.Config.Sites.Enabled {
		a.Logger.Infow("static sites disabled — object store skipped")
		return nil
	}
	oc := a.Config.ObjectStore
	store, err := objectstore.New(objectstore.Config{
		Endpoint:  oc.Endpoint,
		AccessKey: oc.AccessKey,
		SecretKey: oc.SecretKey,
		Bucket:    oc.Bucket,
		Region:    oc.Region,
		UseSSL:    oc.UseSSL,
	})
	if err != nil {
		return fmt.Errorf("object store init: %w", err)
	}
	bctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := objectstore.EnsureBucket(bctx, store, oc.Region); err != nil {
		a.Logger.Warnw("object store bucket check failed", "bucket", oc.Bucket, "error", err)
	}
	a.ObjectStore = store
	return nil
}

func (a *App) initRepositories() {
	a.Logger.Info("itializing repositories...")
	authRepo := repository.NewAuthRepository(a.DB)
	projectRepo := projectRepos.NewProjectRepository(a.DB)
	workloadRepo := workloadsRepo.NewWorkloadRepository(a.DB)

	a.Repos = &RepositoryContainer{
		Auth:     authRepo,
		Project:  projectRepo,
		Workload: workloadRepo,
		Site:     sitesRepo.NewSiteRepository(a.DB),
	}
}

// initDomain : plus de seed de rôles, ils sont définis dans le code
// (internal/iam/roles.go, migration 000007).
func (a *App) initDomain(ctx context.Context) error {
	return nil
}

func (a *App) initTemporalClient() error {
	a.Logger.Info("Connecting to Temporal Server...")

	metricsHandler, metricsCloser := observability.NewTemporalMetricsHandler("temporal")
	a.Observability.TemporalMetricsCloser = metricsCloser

	opts := client.Options{
		HostPort:       a.Config.Temporal.Host,
		Namespace:      a.Config.Temporal.Namespace,
		MetricsHandler: metricsHandler,
	}

	c, err := client.Dial(opts)
	if err != nil {
		return fmt.Errorf("unable to create temporal client: %w", err)
	}

	a.Temporal.Client = c
	a.TemporalDistributor = temporal.NewDistributor(c, a.Config.Temporal.TaskQueue)
	a.Logger.Info("Connected to Temporal successfully")
	return nil
}

func (a *App) initKubernetes() error {
	a.Logger.Info("Initializing Kubernetes Provider...")

	k8sProvider, err := k8sprovider.NewKubernetesProvider(a.Config.Kubernetes.KubeConfigPath)
	if err != nil {
		return err
	}

	a.K8sProvider = k8sProvider

	version, err := k8sProvider.GetServerVersion()
	if err != nil {
		a.Logger.Warnw("K8s ping failed", "error", err)
	} else {
		a.Logger.Infow("Connected to Kubernetes", "version", version)
	}

	return nil
}

// initSTS : trousseau de signature (chiffré par la KEK), rotation en tâche de
// fond, vérification des jetons par le middleware.
func (a *App) initSTS() error {
	issuer := strings.TrimRight(strings.TrimSpace(a.Config.IAM.STSIssuer), "/")
	if issuer == "" {
		return fmt.Errorf("iam: sts_issuer is required (KUBEMANAGER_IAM_STS_ISSUER, ex. https://api.example.com/kmanager/v1/iam)")
	}
	keys, err := sts.NewKeyRing(a.DB, a.Config.IAM.STSKeyEncryptionKey)
	if err != nil {
		return fmt.Errorf("iam: %w (set iam.sts_key_encryption_key / KUBEMANAGER_IAM_STS_KEY_ENCRYPTION_KEY)", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := keys.Rotate(ctx); err != nil {
		return fmt.Errorf("iam: sts signing keys: %w", err)
	}
	a.STS = sts.NewService(issuer, keys, a.DB)
	a.ServiceAccounts.TokenURI = a.STS.TokenURI()
	a.Security.Middleware.SetTokenVerifier(a.STS)

	logErr := func(err error) { a.Logger.Errorw("sts background task failed", "error", err) }
	go keys.Run(context.Background(), logErr)
	go func() {
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for range t.C {
			if err := a.STS.PurgeUsedAssertions(context.Background()); err != nil {
				logErr(err)
			}
		}
	}()
	return nil
}

func (a *App) stepUpMaxAge() time.Duration {
	if a.Config.IAM.StepUpMaxAge > 0 {
		return a.Config.IAM.StepUpMaxAge
	}
	return security.DefaultStepUpMaxAge
}

package proxy

import (
	"context"

	configv3 "skywalking.apache.org/repo/goapi/collect/agent/configuration/v3"
	commonv3 "skywalking.apache.org/repo/goapi/collect/common/v3"
	accessv3 "skywalking.apache.org/repo/goapi/collect/ebpf/accesslog/v3"
	processv3 "skywalking.apache.org/repo/goapi/collect/ebpf/profiling/process/v3"
	ebpfv3 "skywalking.apache.org/repo/goapi/collect/ebpf/profiling/v3"
	eventv3 "skywalking.apache.org/repo/goapi/collect/event/v3"
	agentv3 "skywalking.apache.org/repo/goapi/collect/language/agent/v3"
	profilev3 "skywalking.apache.org/repo/goapi/collect/language/profile/v3"
	logv3 "skywalking.apache.org/repo/goapi/collect/logging/v3"
	managementv3 "skywalking.apache.org/repo/goapi/collect/management/v3"
	meshv3 "skywalking.apache.org/repo/goapi/collect/servicemesh/v3"

	"google.golang.org/grpc"
)

const (
	methodTraceCollect         = "/skywalking.v3.TraceSegmentReportService/collect"
	methodTraceCollectInSync   = "/skywalking.v3.TraceSegmentReportService/collectInSync"
	methodSpanEventCollect     = "/skywalking.v3.SpanAttachedEventReportService/collect"
	methodManagementProperties = "/skywalking.v3.ManagementService/reportInstanceProperties"
	methodManagementKeepAlive  = "/skywalking.v3.ManagementService/keepAlive"
	methodJVMCollect           = "/skywalking.v3.JVMMetricReportService/collect"
	methodCLRCollect           = "/skywalking.v3.CLRMetricReportService/collect"
	methodMeterCollect         = "/skywalking.v3.MeterReportService/collect"
	methodMeterCollectBatch    = "/skywalking.v3.MeterReportService/collectBatch"
	methodLogCollect           = "/skywalking.v3.LogReportService/collect"
	methodEventCollect         = "/skywalking.v3.EventService/collect"
	methodBrowserPerf          = "/skywalking.v3.BrowserPerfService/collectPerfData"
	methodBrowserVitals        = "/skywalking.v3.BrowserPerfService/collectWebVitalsPerfData"
	methodBrowserResource      = "/skywalking.v3.BrowserPerfService/collectResourcePerfData"
	methodBrowserInteractions  = "/skywalking.v3.BrowserPerfService/collectWebInteractionsPerfData"
	methodBrowserErrors        = "/skywalking.v3.BrowserPerfService/collectErrorLogs"
	methodMeshCollect          = "/skywalking.v3.ServiceMeshMetricService/collect"
	methodAccessLogCollect     = "/skywalking.v3.EBPFAccessLogService/collect"
	methodProcessReport        = "/skywalking.v3.EBPFProcessService/reportProcesses"
	methodProcessKeepAlive     = "/skywalking.v3.EBPFProcessService/keepAlive"
	methodConfigurationFetch   = "/skywalking.v3.ConfigurationDiscoveryService/fetchConfigurations"
	methodProfileCommands      = "/skywalking.v3.ProfileTask/getProfileTaskCommands"
	methodProfileSnapshot      = "/skywalking.v3.ProfileTask/collectSnapshot"
	methodGoProfileReport      = "/skywalking.v3.ProfileTask/goProfileReport"
	methodProfileFinish        = "/skywalking.v3.ProfileTask/reportTaskFinish"
	methodEBPFQuery            = "/skywalking.v3.EBPFProfilingService/queryTasks"
	methodEBPFCollect          = "/skywalking.v3.EBPFProfilingService/collectProfilingData"
	methodContinuousPolicies   = "/skywalking.v3.ContinuousProfilingService/queryPolicies"
	methodContinuousReport     = "/skywalking.v3.ContinuousProfilingService/reportProfilingTask"
)

// RegisterServices registers exactly the 16 official v3 services in the MVP
// contract. grpc-go itself returns UNIMPLEMENTED for every other method.
func (p *Proxy) RegisterServices(registrar grpc.ServiceRegistrar) {
	agentv3.RegisterTraceSegmentReportServiceServer(registrar, &traceService{p: p})
	agentv3.RegisterSpanAttachedEventReportServiceServer(registrar, &spanEventService{p: p})
	managementv3.RegisterManagementServiceServer(registrar, &managementService{p: p})
	agentv3.RegisterJVMMetricReportServiceServer(registrar, &jvmService{p: p})
	agentv3.RegisterCLRMetricReportServiceServer(registrar, &clrService{p: p})
	agentv3.RegisterMeterReportServiceServer(registrar, &meterService{p: p})
	logv3.RegisterLogReportServiceServer(registrar, &logService{p: p})
	eventv3.RegisterEventServiceServer(registrar, &eventService{p: p})
	agentv3.RegisterBrowserPerfServiceServer(registrar, &browserService{p: p})
	meshv3.RegisterServiceMeshMetricServiceServer(registrar, &meshService{p: p})
	accessv3.RegisterEBPFAccessLogServiceServer(registrar, &accessLogService{p: p})
	processv3.RegisterEBPFProcessServiceServer(registrar, &processService{p: p})
	configv3.RegisterConfigurationDiscoveryServiceServer(registrar, &configurationService{p: p})
	profilev3.RegisterProfileTaskServer(registrar, &profileService{p: p})
	ebpfv3.RegisterEBPFProfilingServiceServer(registrar, &ebpfProfileService{p: p})
	ebpfv3.RegisterContinuousProfilingServiceServer(registrar, &continuousService{p: p})
}

type traceService struct {
	agentv3.UnimplementedTraceSegmentReportServiceServer
	p *Proxy
}

func (s *traceService) Collect(stream agentv3.TraceSegmentReportService_CollectServer) error {
	return relayClientStream(s.p, methodTraceCollect, stream, s.p.oap.trace.Collect, s.p.arms.trace.Collect, true)
}

func (s *traceService) CollectInSync(ctx context.Context, request *agentv3.SegmentCollection) (*commonv3.Commands, error) {
	return relayUnary(s.p, ctx, methodTraceCollectInSync, request, s.p.oap.trace.CollectInSync, s.p.arms.trace.CollectInSync, true)
}

type spanEventService struct {
	agentv3.UnimplementedSpanAttachedEventReportServiceServer
	p *Proxy
}

func (s *spanEventService) Collect(stream agentv3.SpanAttachedEventReportService_CollectServer) error {
	return relayClientStream(s.p, methodSpanEventCollect, stream, s.p.oap.spanEvent.Collect, s.p.arms.spanEvent.Collect, true)
}

type managementService struct {
	managementv3.UnimplementedManagementServiceServer
	p *Proxy
}

func (s *managementService) ReportInstanceProperties(ctx context.Context, request *managementv3.InstanceProperties) (*commonv3.Commands, error) {
	return relayUnary(s.p, ctx, methodManagementProperties, request, s.p.oap.management.ReportInstanceProperties, s.p.arms.management.ReportInstanceProperties, true)
}

func (s *managementService) KeepAlive(ctx context.Context, request *managementv3.InstancePingPkg) (*commonv3.Commands, error) {
	return relayUnary(s.p, ctx, methodManagementKeepAlive, request, s.p.oap.management.KeepAlive, s.p.arms.management.KeepAlive, true)
}

type jvmService struct {
	agentv3.UnimplementedJVMMetricReportServiceServer
	p *Proxy
}

func (s *jvmService) Collect(ctx context.Context, request *agentv3.JVMMetricCollection) (*commonv3.Commands, error) {
	return relayUnary(s.p, ctx, methodJVMCollect, request, s.p.oap.jvm.Collect, s.p.arms.jvm.Collect, true)
}

type clrService struct {
	agentv3.UnimplementedCLRMetricReportServiceServer
	p *Proxy
}

func (s *clrService) Collect(ctx context.Context, request *agentv3.CLRMetricCollection) (*commonv3.Commands, error) {
	return relayUnary(s.p, ctx, methodCLRCollect, request, s.p.oap.clr.Collect, s.p.arms.clr.Collect, true)
}

type meterService struct {
	agentv3.UnimplementedMeterReportServiceServer
	p *Proxy
}

func (s *meterService) Collect(stream agentv3.MeterReportService_CollectServer) error {
	return relayClientStream(s.p, methodMeterCollect, stream, s.p.oap.meter.Collect, s.p.arms.meter.Collect, true)
}

func (s *meterService) CollectBatch(stream agentv3.MeterReportService_CollectBatchServer) error {
	return relayClientStream(s.p, methodMeterCollectBatch, stream, s.p.oap.meter.CollectBatch, s.p.arms.meter.CollectBatch, true)
}

type logService struct {
	logv3.UnimplementedLogReportServiceServer
	p *Proxy
}

func (s *logService) Collect(stream logv3.LogReportService_CollectServer) error {
	return relayClientStream(s.p, methodLogCollect, stream, s.p.oap.log.Collect, s.p.arms.log.Collect, true)
}

type eventService struct {
	eventv3.UnimplementedEventServiceServer
	p *Proxy
}

func (s *eventService) Collect(stream eventv3.EventService_CollectServer) error {
	return relayClientStream(s.p, methodEventCollect, stream, s.p.oap.event.Collect, s.p.arms.event.Collect, true)
}

type browserService struct {
	agentv3.UnimplementedBrowserPerfServiceServer
	p *Proxy
}

func (s *browserService) CollectPerfData(ctx context.Context, request *agentv3.BrowserPerfData) (*commonv3.Commands, error) {
	return relayUnary(s.p, ctx, methodBrowserPerf, request, s.p.oap.browser.CollectPerfData, s.p.arms.browser.CollectPerfData, true)
}

func (s *browserService) CollectWebVitalsPerfData(ctx context.Context, request *agentv3.BrowserWebVitalsPerfData) (*commonv3.Commands, error) {
	return relayUnary(s.p, ctx, methodBrowserVitals, request, s.p.oap.browser.CollectWebVitalsPerfData, s.p.arms.browser.CollectWebVitalsPerfData, true)
}

func (s *browserService) CollectResourcePerfData(ctx context.Context, request *agentv3.BrowserResourcePerfData) (*commonv3.Commands, error) {
	return relayUnary(s.p, ctx, methodBrowserResource, request, s.p.oap.browser.CollectResourcePerfData, s.p.arms.browser.CollectResourcePerfData, true)
}

func (s *browserService) CollectWebInteractionsPerfData(ctx context.Context, request *agentv3.BrowserWebInteractionsPerfData) (*commonv3.Commands, error) {
	return relayUnary(s.p, ctx, methodBrowserInteractions, request, s.p.oap.browser.CollectWebInteractionsPerfData, s.p.arms.browser.CollectWebInteractionsPerfData, true)
}

func (s *browserService) CollectErrorLogs(stream agentv3.BrowserPerfService_CollectErrorLogsServer) error {
	return relayClientStream(s.p, methodBrowserErrors, stream, s.p.oap.browser.CollectErrorLogs, s.p.arms.browser.CollectErrorLogs, true)
}

type meshService struct {
	meshv3.UnimplementedServiceMeshMetricServiceServer
	p *Proxy
}

func (s *meshService) Collect(stream meshv3.ServiceMeshMetricService_CollectServer) error {
	return relayClientStream(s.p, methodMeshCollect, stream, s.p.oap.mesh.Collect, s.p.arms.mesh.Collect, true)
}

type accessLogService struct {
	accessv3.UnimplementedEBPFAccessLogServiceServer
	p *Proxy
}

func (s *accessLogService) Collect(stream accessv3.EBPFAccessLogService_CollectServer) error {
	return relayClientStream(s.p, methodAccessLogCollect, stream, s.p.oap.accessLog.Collect, s.p.arms.accessLog.Collect, true)
}

type processService struct {
	processv3.UnimplementedEBPFProcessServiceServer
	p *Proxy
}

func (s *processService) ReportProcesses(ctx context.Context, request *processv3.EBPFProcessReportList) (*processv3.EBPFReportProcessDownstream, error) {
	return relayUnary(s.p, ctx, methodProcessReport, request, s.p.oap.process.ReportProcesses, s.p.arms.process.ReportProcesses, true)
}

func (s *processService) KeepAlive(ctx context.Context, request *processv3.EBPFProcessPingPkgList) (*commonv3.Commands, error) {
	return relayUnary(s.p, ctx, methodProcessKeepAlive, request, s.p.oap.process.KeepAlive, s.p.arms.process.KeepAlive, true)
}

type configurationService struct {
	configv3.UnimplementedConfigurationDiscoveryServiceServer
	p *Proxy
}

func (s *configurationService) FetchConfigurations(ctx context.Context, request *configv3.ConfigurationSyncRequest) (*commonv3.Commands, error) {
	return relayUnary(s.p, ctx, methodConfigurationFetch, request, s.p.oap.configuration.FetchConfigurations, s.p.oap.configuration.FetchConfigurations, false)
}

type profileService struct {
	profilev3.UnimplementedProfileTaskServer
	p *Proxy
}

func (s *profileService) GetProfileTaskCommands(ctx context.Context, request *profilev3.ProfileTaskCommandQuery) (*commonv3.Commands, error) {
	return relayUnary(s.p, ctx, methodProfileCommands, request, s.p.oap.profile.GetProfileTaskCommands, s.p.oap.profile.GetProfileTaskCommands, false)
}

func (s *profileService) CollectSnapshot(stream profilev3.ProfileTask_CollectSnapshotServer) error {
	return relayClientStream(s.p, methodProfileSnapshot, stream, s.p.oap.profile.CollectSnapshot, s.p.oap.profile.CollectSnapshot, false)
}

func (s *profileService) GoProfileReport(stream profilev3.ProfileTask_GoProfileReportServer) error {
	return relayClientStream(s.p, methodGoProfileReport, stream, s.p.oap.profile.GoProfileReport, s.p.oap.profile.GoProfileReport, false)
}

func (s *profileService) ReportTaskFinish(ctx context.Context, request *profilev3.ProfileTaskFinishReport) (*commonv3.Commands, error) {
	return relayUnary(s.p, ctx, methodProfileFinish, request, s.p.oap.profile.ReportTaskFinish, s.p.oap.profile.ReportTaskFinish, false)
}

type ebpfProfileService struct {
	ebpfv3.UnimplementedEBPFProfilingServiceServer
	p *Proxy
}

func (s *ebpfProfileService) QueryTasks(ctx context.Context, request *ebpfv3.EBPFProfilingTaskQuery) (*commonv3.Commands, error) {
	return relayUnary(s.p, ctx, methodEBPFQuery, request, s.p.oap.ebpfProfile.QueryTasks, s.p.oap.ebpfProfile.QueryTasks, false)
}

func (s *ebpfProfileService) CollectProfilingData(stream ebpfv3.EBPFProfilingService_CollectProfilingDataServer) error {
	return relayClientStream(s.p, methodEBPFCollect, stream, s.p.oap.ebpfProfile.CollectProfilingData, s.p.oap.ebpfProfile.CollectProfilingData, false)
}

type continuousService struct {
	ebpfv3.UnimplementedContinuousProfilingServiceServer
	p *Proxy
}

func (s *continuousService) QueryPolicies(ctx context.Context, request *ebpfv3.ContinuousProfilingPolicyQuery) (*commonv3.Commands, error) {
	return relayUnary(s.p, ctx, methodContinuousPolicies, request, s.p.oap.continuous.QueryPolicies, s.p.oap.continuous.QueryPolicies, false)
}

func (s *continuousService) ReportProfilingTask(ctx context.Context, request *ebpfv3.ContinuousProfilingReport) (*commonv3.Commands, error) {
	return relayUnary(s.p, ctx, methodContinuousReport, request, s.p.oap.continuous.ReportProfilingTask, s.p.oap.continuous.ReportProfilingTask, false)
}

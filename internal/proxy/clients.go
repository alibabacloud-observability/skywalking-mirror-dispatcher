package proxy

import (
	configv3 "skywalking.apache.org/repo/goapi/collect/agent/configuration/v3"
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

type targetClients struct {
	configuration configv3.ConfigurationDiscoveryServiceClient
	accessLog     accessv3.EBPFAccessLogServiceClient
	process       processv3.EBPFProcessServiceClient
	ebpfProfile   ebpfv3.EBPFProfilingServiceClient
	continuous    ebpfv3.ContinuousProfilingServiceClient
	event         eventv3.EventServiceClient
	trace         agentv3.TraceSegmentReportServiceClient
	spanEvent     agentv3.SpanAttachedEventReportServiceClient
	jvm           agentv3.JVMMetricReportServiceClient
	clr           agentv3.CLRMetricReportServiceClient
	meter         agentv3.MeterReportServiceClient
	browser       agentv3.BrowserPerfServiceClient
	profile       profilev3.ProfileTaskClient
	log           logv3.LogReportServiceClient
	management    managementv3.ManagementServiceClient
	mesh          meshv3.ServiceMeshMetricServiceClient
}

func newTargetClients(conn grpc.ClientConnInterface) targetClients {
	return targetClients{
		configuration: configv3.NewConfigurationDiscoveryServiceClient(conn),
		accessLog:     accessv3.NewEBPFAccessLogServiceClient(conn),
		process:       processv3.NewEBPFProcessServiceClient(conn),
		ebpfProfile:   ebpfv3.NewEBPFProfilingServiceClient(conn),
		continuous:    ebpfv3.NewContinuousProfilingServiceClient(conn),
		event:         eventv3.NewEventServiceClient(conn),
		trace:         agentv3.NewTraceSegmentReportServiceClient(conn),
		spanEvent:     agentv3.NewSpanAttachedEventReportServiceClient(conn),
		jvm:           agentv3.NewJVMMetricReportServiceClient(conn),
		clr:           agentv3.NewCLRMetricReportServiceClient(conn),
		meter:         agentv3.NewMeterReportServiceClient(conn),
		browser:       agentv3.NewBrowserPerfServiceClient(conn),
		profile:       profilev3.NewProfileTaskClient(conn),
		log:           logv3.NewLogReportServiceClient(conn),
		management:    managementv3.NewManagementServiceClient(conn),
		mesh:          meshv3.NewServiceMeshMetricServiceClient(conn),
	}
}

// Package policy defines the supported SkyWalking v3 method surface.
package policy

// Route controls whether an accepted method is also offered to ARMS.
type Route uint8

const (
	OAPOnly Route = iota
	MirrorToARMS
)

// Method routes are deliberately explicit. Unknown methods are not registered
// on the gRPC server and therefore receive UNIMPLEMENTED from grpc-go.
var Methods = map[string]Route{
	"/skywalking.v3.TraceSegmentReportService/collect":                 MirrorToARMS,
	"/skywalking.v3.TraceSegmentReportService/collectInSync":           MirrorToARMS,
	"/skywalking.v3.SpanAttachedEventReportService/collect":            MirrorToARMS,
	"/skywalking.v3.ManagementService/reportInstanceProperties":        MirrorToARMS,
	"/skywalking.v3.ManagementService/keepAlive":                       MirrorToARMS,
	"/skywalking.v3.JVMMetricReportService/collect":                    MirrorToARMS,
	"/skywalking.v3.CLRMetricReportService/collect":                    MirrorToARMS,
	"/skywalking.v3.MeterReportService/collect":                        MirrorToARMS,
	"/skywalking.v3.MeterReportService/collectBatch":                   MirrorToARMS,
	"/skywalking.v3.LogReportService/collect":                          MirrorToARMS,
	"/skywalking.v3.EventService/collect":                              MirrorToARMS,
	"/skywalking.v3.BrowserPerfService/collectPerfData":                MirrorToARMS,
	"/skywalking.v3.BrowserPerfService/collectWebVitalsPerfData":       MirrorToARMS,
	"/skywalking.v3.BrowserPerfService/collectResourcePerfData":        MirrorToARMS,
	"/skywalking.v3.BrowserPerfService/collectWebInteractionsPerfData": MirrorToARMS,
	"/skywalking.v3.BrowserPerfService/collectErrorLogs":               MirrorToARMS,
	"/skywalking.v3.ServiceMeshMetricService/collect":                  MirrorToARMS,
	"/skywalking.v3.EBPFAccessLogService/collect":                      MirrorToARMS,
	"/skywalking.v3.EBPFProcessService/reportProcesses":                MirrorToARMS,
	"/skywalking.v3.EBPFProcessService/keepAlive":                      MirrorToARMS,
	"/skywalking.v3.ConfigurationDiscoveryService/fetchConfigurations": OAPOnly,
	"/skywalking.v3.ProfileTask/getProfileTaskCommands":                OAPOnly,
	"/skywalking.v3.ProfileTask/collectSnapshot":                       OAPOnly,
	"/skywalking.v3.ProfileTask/goProfileReport":                       OAPOnly,
	"/skywalking.v3.ProfileTask/reportTaskFinish":                      OAPOnly,
	"/skywalking.v3.EBPFProfilingService/queryTasks":                   OAPOnly,
	"/skywalking.v3.EBPFProfilingService/collectProfilingData":         OAPOnly,
	"/skywalking.v3.ContinuousProfilingService/queryPolicies":          OAPOnly,
	"/skywalking.v3.ContinuousProfilingService/reportProfilingTask":    OAPOnly,
}

func Lookup(method string) (Route, bool) {
	route, ok := Methods[method]
	return route, ok
}

func Mirrored(method string) bool {
	route, ok := Lookup(method)
	return ok && route == MirrorToARMS
}

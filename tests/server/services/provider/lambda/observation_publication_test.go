package lambda_test

import (
	"context"
	"errors"
	"fmt"
	"github.com/d31ma/heimdall/app/server/models/spec"
	"github.com/d31ma/heimdall/app/server/services/provider"
	"github.com/d31ma/heimdall/app/server/services/provider/lambda"
	"io"
	"strings"
	"testing"
	"time"
)

func TestLambdaFunctionMetricsRejectPartialAndMalformedNativeResults(t *testing.T) {
	for _, fault := range []string{"partial", "internal", "unknown", "identifier", "length", "nan", "infinity", "missing", "empty"} {
		t.Run(fault, func(t *testing.T) {
			h, fake, adapter, plan := deployed(t)
			fake.mu.Lock()
			fake.metricFault = fault
			fake.mu.Unlock()
			ref := provider.FunctionRef{AppRef: plan.App, Service: "handler", Version: "1"}
			series, err := adapter.FunctionMetrics(context.Background(), h.Target, ref, provider.Window{})
			var diagnostic *lambda.Diagnostic
			if !errors.As(err, &diagnostic) || diagnostic.Code != "HD0417" {
				t.Fatalf("native %s response accepted: %+v %v", fault, series, err)
			}
			if len(series.Invocations)+len(series.Errors)+len(series.Throttles)+len(series.DurationMS) != 0 {
				t.Fatalf("incomplete observation escaped as valid series: %+v", series)
			}
		})
	}
}

func TestLambdaFunctionMetricsBoundNativePagination(t *testing.T) {
	for _, mode := range []string{"repeat", "exhaust"} {
		t.Run(mode, func(t *testing.T) {
			h, fake, adapter, plan := deployed(t)
			fake.mu.Lock()
			fake.metricPageMode = mode
			fake.mu.Unlock()
			ref := provider.FunctionRef{AppRef: plan.App, Service: "handler", Version: "1"}
			_, err := adapter.FunctionMetrics(context.Background(), h.Target, ref, provider.Window{})
			var diagnostic *lambda.Diagnostic
			if !errors.As(err, &diagnostic) || diagnostic.Code != "HD0417" {
				t.Fatalf("unbounded metrics pagination: %v", err)
			}
			fake.mu.Lock()
			defer fake.mu.Unlock()
			expected := 2
			if mode == "exhaust" {
				expected = 20
			}
			if fake.metricRequests != expected {
				t.Fatalf("metrics read %d pages, want %d", fake.metricRequests, expected)
			}
		})
	}
}

func TestLambdaFunctionObservationRejectsMutableVersionBeforeNativeAccess(t *testing.T) {
	for _, version := range []string{"", "$LATEST", "0", "-1"} {
		t.Run(version, func(t *testing.T) {
			h, fake, adapter, plan := deployed(t)
			fake.mu.Lock()
			before := fake.requests
			fake.mu.Unlock()
			ref := provider.FunctionRef{AppRef: plan.App, Service: "handler", Version: version}
			if _, err := adapter.FunctionMetrics(context.Background(), h.Target, ref, provider.Window{}); err == nil {
				t.Fatal("mutable observation accepted")
			}
			if _, err := adapter.FunctionLogs(context.Background(), h.Target, ref, provider.LogFilter{}); err == nil {
				t.Fatal("mutable log observation accepted")
			}
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if fake.requests != before {
				t.Fatal("invalid observation touched native SDK")
			}
		})
	}
}

func TestLambdaFunctionLogsBoundNativeQueries(t *testing.T) {
	for _, fault := range []string{"stream-repeat", "log-repeat", "stream-limit", "byte-limit"} {
		t.Run(fault, func(t *testing.T) {
			h, fake, adapter, plan := deployed(t)
			fake.mu.Lock()
			switch fault {
			case "stream-repeat":
				fake.streamNext = "again"
			case "log-repeat":
				fake.logNext = "again"
			case "stream-limit":
				fake.streamNames = []string{}
				for index := 0; index < 101; index++ {
					fake.streamNames = append(fake.streamNames, fmt.Sprintf("2026/10/03/[1]event%d", index))
				}
			case "byte-limit":
				fake.logMessages = []string{strings.Repeat("a", 4*1024*1024)}
			}
			fake.mu.Unlock()
			ref := provider.FunctionRef{AppRef: plan.App, Service: "handler", Version: "1"}
			reader, err := adapter.FunctionLogs(context.Background(), h.Target, ref, provider.LogFilter{})
			var diagnostic *lambda.Diagnostic
			if !errors.As(err, &diagnostic) || diagnostic.Code != "HD0417" {
				t.Fatalf("unbounded native logs %s: %v", fault, err)
			}
			if reader != nil {
				reader.Close()
				t.Fatal("failed observation returned log reader")
			}
		})
	}
}

func TestLambdaFunctionLogsWithoutVersionStreamsDoNotQueryEvents(t *testing.T) {
	h, fake, adapter, plan := deployed(t)
	fake.mu.Lock()
	fake.streamNames = []string{"2026/10/03/[2]other"}
	fake.mu.Unlock()
	ref := provider.FunctionRef{AppRef: plan.App, Service: "handler", Version: "1"}
	reader, err := adapter.FunctionLogs(context.Background(), h.Target, ref, provider.LogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	output, err := io.ReadAll(reader)
	if err != nil || len(output) != 0 {
		t.Fatalf("empty version streams: %s %v", output, err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.logRequests != 0 {
		t.Fatal("empty version query read other native log streams")
	}
}

func TestLambdaPublicationRechecksImmutableReadbackFields(t *testing.T) {
	for _, field := range []string{"CodeSha256", "Timeout", "Version", "State", "Image", "Environment"} {
		t.Run(field, func(t *testing.T) {
			h, fake, adapter, plan := deployed(t)
			name := (lambda.Identity{App: plan.App, Service: "handler"}).Name()
			fake.mu.Lock()
			listed := fake.clone(fake.versions[name]["1"])
			fake.publicationConflict = true
			fake.versionPages = []map[string]any{{"Versions": []map[string]any{listed}}}
			switch field {
			case "CodeSha256":
				fake.versions[name]["1"][field] = "different-code-hash"
			case "Timeout":
				fake.versions[name]["1"][field] = 60
			case "Version":
				fake.versions[name]["1"][field] = "2"
			case "State":
				fake.versions[name]["1"][field] = "Inactive"
			case "Environment":
				environment := fake.versions[name]["1"]["Environment"].(map[string]any)
				variables := environment["Variables"].(map[string]any)
				variables["PASSWORD"] = "different-fixture-secret"
			case "Image":
				fake.versionImages[name]["1"] = strings.Replace(fake.versionImages[name]["1"], strings.Repeat("a", 64), strings.Repeat("b", 64), 1)
			}
			fake.mu.Unlock()
			want := functionSpec("metadata-readback")
			want.Services[0].Labels = []spec.Label{{Key: "owner", Value: "team"}}
			ctx := context.Background()
			candidate, err := adapter.Plan(ctx, h.Target, want)
			if err != nil {
				t.Fatal(err)
			}
			result, err := adapter.Apply(h.ApplyContext(ctx, want), h.Target, candidate)
			if err != nil || !strings.Contains(result.Failures["handler"], "no matching immutable version") {
				t.Fatalf("wrong immutable %s reused: %+v %v", field, result, err)
			}
		})
	}
}

func TestLambdaPublicationSearchesNativePagesForMatchingVersion(t *testing.T) {
	h, fake, adapter, plan := deployed(t)
	name := (lambda.Identity{App: plan.App, Service: "handler"}).Name()
	fake.mu.Lock()
	correct := fake.clone(fake.versions[name]["1"])
	wrong := fake.clone(correct)
	wrong["Timeout"] = 60
	fake.publicationConflict = true
	fake.versionPages = []map[string]any{{"Versions": []map[string]any{wrong}, "NextMarker": "next"}, {"Versions": []map[string]any{correct}}}
	fake.mu.Unlock()
	want := functionSpec("metadata-pagination")
	want.Services[0].Labels = []spec.Label{{Key: "owner", Value: "team"}}
	ctx := context.Background()
	candidate, err := adapter.Plan(ctx, h.Target, want)
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Apply(h.ApplyContext(ctx, want), h.Target, candidate)
	if err != nil || len(result.Failures) != 0 {
		t.Fatalf("matching immutable second page rejected: %+v %v", result, err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.versionRequests != 2 {
		t.Fatalf("read %d version pages, want 2", fake.versionRequests)
	}
}

func TestLambdaPublicationBoundsNativeVersionPagination(t *testing.T) {
	for _, mode := range []string{"repeat", "exhaust"} {
		t.Run(mode, func(t *testing.T) {
			h, fake, adapter, plan := deployed(t)
			name := (lambda.Identity{App: plan.App, Service: "handler"}).Name()
			fake.mu.Lock()
			wrong := fake.clone(fake.versions[name]["1"])
			wrong["Timeout"] = 60
			fake.publicationConflict = true
			if mode == "repeat" {
				fake.versionPages = []map[string]any{{"Versions": []map[string]any{wrong}, "NextMarker": "again"}}
			}
			if mode == "exhaust" {
				for index := 0; index < 101; index++ {
					fake.versionPages = append(fake.versionPages, map[string]any{"Versions": []map[string]any{wrong}, "NextMarker": fmt.Sprint(index + 1)})
				}
			}
			fake.mu.Unlock()
			want := functionSpec("metadata-bound")
			want.Services[0].Labels = []spec.Label{{Key: "owner", Value: "team"}}
			ctx := context.Background()
			candidate, err := adapter.Plan(ctx, h.Target, want)
			if err != nil {
				t.Fatal(err)
			}
			result, err := adapter.Apply(h.ApplyContext(ctx, want), h.Target, candidate)
			expected := "pagination did not advance"
			expectedPages := 2
			if mode == "exhaust" {
				expected, expectedPages = "inventory exceeded 100 pages", 100
			}
			if err != nil || !strings.Contains(result.Failures["handler"], expected) {
				t.Fatalf("unbounded version search: %+v %v", result, err)
			}
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if fake.versionRequests != expectedPages {
				t.Fatalf("read %d version pages, want %d", fake.versionRequests, expectedPages)
			}
		})
	}
}

func TestLambdaFunctionObservationVerifiesOwnedImmutableReadback(t *testing.T) {
	for _, fault := range []string{"ownership", "version"} {
		t.Run(fault, func(t *testing.T) {
			h, fake, adapter, plan := deployed(t)
			name := (lambda.Identity{App: plan.App, Service: "handler"}).Name()
			fake.mu.Lock()
			if fault == "ownership" {
				fake.tags[name]["dev.delma.heimdall.managed-by"] = "another-owner"
			}
			if fault == "version" {
				fake.versions[name]["1"]["Version"] = "2"
			}
			fake.mu.Unlock()
			ref := provider.FunctionRef{AppRef: plan.App, Service: "handler", Version: "1"}
			_, metricsError := adapter.FunctionMetrics(context.Background(), h.Target, ref, provider.Window{})
			_, logsError := adapter.FunctionLogs(context.Background(), h.Target, ref, provider.LogFilter{})
			expected := "HD0417"
			if fault == "ownership" {
				expected = "HD0414"
			}
			for _, err := range []error{metricsError, logsError} {
				var diagnostic *lambda.Diagnostic
				if !errors.As(err, &diagnostic) || diagnostic.Code != expected {
					t.Fatalf("%s observation accepted: %v", fault, err)
				}
			}
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if fake.metricRequests+fake.streamRequests+fake.logRequests != 0 {
				t.Fatal("unverified version reached telemetry SDK")
			}
		})
	}
}

func TestLambdaFunctionLogsRespectNativeGroupAndBoundedLineCount(t *testing.T) {
	h, fake, adapter, plan := deployed(t)
	name := (lambda.Identity{App: plan.App, Service: "handler"}).Name()
	fake.mu.Lock()
	fake.versions[name]["1"]["LoggingConfig"] = map[string]any{"LogGroup": "/existing/custom-group"}
	fake.logMessages = []string{"first", "second"}
	fake.logNext = "again"
	fake.mu.Unlock()
	since := time.Now().Add(-time.Hour).UTC()
	ref := provider.FunctionRef{AppRef: plan.App, Service: "handler", Version: "1"}
	reader, err := adapter.FunctionLogs(context.Background(), h.Target, ref, provider.LogFilter{Tail: 1, Since: since})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	output, err := io.ReadAll(reader)
	if err != nil || string(output) != "first\n" {
		t.Fatalf("bounded native output: %q %v", output, err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.logRequests != 1 || fake.logInput["logGroupName"] != "/existing/custom-group" || int64(fake.logInput["startTime"].(float64)) != since.UnixMilli() {
		t.Fatalf("native log filter did not preserve group/window/bound: %+v", fake.logInput)
	}
}

func TestLambdaFunctionMetricsRejectUnboundedWindowsBeforeMetricQuery(t *testing.T) {
	now := time.Now().UTC()
	windows := []provider.Window{{From: now, To: now.Add(-time.Hour)}, {From: now.Add(-25 * time.Hour), To: now}, {From: now.Add(-time.Hour), To: now, Step: 30 * time.Second}, {From: now.Add(-time.Hour), To: now, Step: 2 * time.Hour}, {From: now.Add(-time.Hour), To: now, Step: 90 * time.Second}}
	for index, window := range windows {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			h, fake, adapter, plan := deployed(t)
			ref := provider.FunctionRef{AppRef: plan.App, Service: "handler", Version: "1"}
			_, err := adapter.FunctionMetrics(context.Background(), h.Target, ref, window)
			var diagnostic *lambda.Diagnostic
			if !errors.As(err, &diagnostic) || diagnostic.Code != "HD0417" {
				t.Fatalf("invalid window accepted: %v", err)
			}
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if fake.metricRequests != 0 {
				t.Fatal("invalid window reached native metric query")
			}
		})
	}
}

func TestLambdaObservedVersionAndImageUseSameImmutableDeployment(t *testing.T) {
	h, fake, adapter, plan := deployed(t)
	name := (lambda.Identity{App: plan.App, Service: "handler"}).Name()
	fake.mu.Lock()
	publishedImage := fake.versionImages[name]["1"]
	fake.images[name] = strings.Replace(publishedImage, strings.Repeat("a", 64), strings.Repeat("b", 64), 1)
	if fake.images[name] == publishedImage {
		fake.images[name] = "different-latest-image"
	}
	fake.mu.Unlock()
	live, err := adapter.Observe(context.Background(), h.Target, plan.App)
	if err != nil {
		t.Fatal(err)
	}
	state := live.Services["handler"]
	if state.FunctionVersion != "1" || state.Image != publishedImage {
		t.Fatalf("mixed mutable image and published version: %+v", state)
	}
	wanted := functionSpec("initial")
	drift, err := adapter.Plan(context.Background(), h.Target, wanted)
	if err != nil {
		t.Fatal(err)
	}
	if len(drift.Operations) == 0 || drift.Operations[0].Kind != provider.OpUpdate {
		t.Fatalf("published image projection hid mutable code drift: %+v", drift.Operations)
	}
}

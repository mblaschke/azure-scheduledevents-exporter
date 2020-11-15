package main

import (
	"encoding/json"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"net/http"
	"time"
)

type AzureScheduledEventResponse struct {
	DocumentIncarnation int                   `json:"DocumentIncarnation"`
	Events              []AzureScheduledEvent `json:"Events"`
}

type AzureScheduledEvent struct {
	EventId      string   `json:"EventId"`
	EventType    string   `json:"EventType"`
	ResourceType string   `json:"ResourceType"`
	Resources    []string `json:"Resources"`
	EventStatus  string   `json:"EventStatus"`
	NotBefore    string   `json:"NotBefore"`
}

var (
	scheduledEventDocumentIncarnation = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "azure_scheduledevent_document_incarnation",
			Help: "Azure ScheduledEvent document incarnation",
		},
		[]string{},
	)

	scheduledEvent = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "azure_scheduledevent_event",
			Help: "Azure ScheduledEvent",
		},
		[]string{"eventID", "eventType", "resourceType", "resource", "eventStatus", "notBefore"},
	)

	scheduledEventRequest = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "azure_scheduledevent_request",
			Help: "Azure ScheduledEvent requests",
		},
		[]string{},
	)

	scheduledEventRequestError = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "azure_scheduledevent_request_error",
			Help: "Azure ScheduledEvent failed requests",
		},
		[]string{},
	)

	timeFormatList = []string{
		time.RFC3339,
		time.RFC1123,
		time.RFC822Z,
		time.RFC850,
	}

	httpClient *http.Client

	apiErrorCount = 0
)

func setupMetricsCollection() {
	prometheus.MustRegister(scheduledEvent)
	prometheus.MustRegister(scheduledEventDocumentIncarnation)
	prometheus.MustRegister(scheduledEventRequest)
	prometheus.MustRegister(scheduledEventRequestError)

	apiErrorCount = 0

	// Init http client
	httpClient = &http.Client{
		Timeout: opts.ApiTimeout,
	}
}

func startMetricsCollection() {
	go func() {
		for {
			go probeCollect()
			time.Sleep(opts.ScrapeTime)
		}
	}()
}

func startHttpServer() {
	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe(opts.ServerBind, nil))
}

func probeCollect() {
	scheduledEvents, err := fetchApiUrl()
	if err != nil {
		apiErrorCount++

		if opts.ApiErrorThreshold <= 0 || apiErrorCount <= opts.ApiErrorThreshold {
			log.Errorf("failed API call: %v", err)
			return
		} else {
			log.Panic(err)
		}
	}

	// reset error count and metrics
	apiErrorCount = 0
	scheduledEvent.Reset()

	for _, event := range scheduledEvents.Events {
		eventValue := float64(1)

		if event.NotBefore != "" {
			notBefore, err := parseTime(event.NotBefore)
			if err == nil {
				eventValue = float64(notBefore.Unix())
			} else {
				log.Errorf("failed API call: %v", err)
				log.Errorf("unable to parse time \"%s\" of eventid \"%v\": %v", event.NotBefore, event.EventId, err)
				eventValue = 0
			}
		}

		if len(event.Resources) >= 1 {
			for _, resource := range event.Resources {
				scheduledEvent.With(
					prometheus.Labels{
						"eventID":      event.EventId,
						"eventType":    event.EventType,
						"resourceType": event.ResourceType,
						"resource":     resource,
						"eventStatus":  event.EventStatus,
						"notBefore":    event.NotBefore,
					}).Set(eventValue)
			}
		} else {
			scheduledEvent.With(
				prometheus.Labels{
					"eventID":      event.EventId,
					"eventType":    event.EventType,
					"resourceType": event.ResourceType,
					"resource":     "",
					"eventStatus":  event.EventStatus,
					"notBefore":    event.NotBefore,
				}).Set(eventValue)
		}
	}

	scheduledEventDocumentIncarnation.With(prometheus.Labels{}).Set(float64(scheduledEvents.DocumentIncarnation))

	log.Debugf("fetched %v Azure ScheduledEvents", len(scheduledEvents.Events))
}

func fetchApiUrl() (*AzureScheduledEventResponse, error) {
	ret := &AzureScheduledEventResponse{}

	startTime := time.Now()
	req, err := http.NewRequest("GET", opts.ApiUrl, nil)
	if err != nil {
		scheduledEventRequestError.With(prometheus.Labels{}).Inc()
		return nil, err
	}
	req.Header.Add("Metadata", "true")

	resp, err := httpClient.Do(req)
	if err != nil {
		scheduledEventRequestError.With(prometheus.Labels{}).Inc()
		return nil, err
	}
	defer resp.Body.Close()

	err = json.NewDecoder(resp.Body).Decode(&ret)
	if err != nil {
		scheduledEventRequestError.With(prometheus.Labels{}).Inc()
		return nil, err
	}

	if opts.MetricsRequestStats {
		duration := time.Now().Sub(startTime)
		scheduledEventRequest.With(prometheus.Labels{}).Observe(duration.Seconds())
	}

	return ret, nil
}

func parseTime(value string) (parsedTime time.Time, err error) {
	for _, format := range timeFormatList {
		parsedTime, err = time.Parse(format, value)
		if err == nil {
			break
		}
	}

	return
}

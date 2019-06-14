package main

import (
	"log"
	"fmt"
	"time"
	"net/http"
	"encoding/json"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type AzureScheduledEventResponse struct {
	DocumentIncarnation int `json:"DocumentIncarnation"`
	Events []AzureScheduledEvent `json:"Events"`
}

type AzureScheduledEvent struct {
	EventId string `json:"EventId"`
	EventType string `json:"EventType"`
	ResourceType string `json:"ResourceType"`
	Resources []string `json:"Resources"`
	EventStatus string `json:"EventStatus"`
	NotBefore string `json:"NotBefore"`
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
			ErrorLogger.Error("Failed API call:", err)
			return
		} else {
			panic(err.Error())
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
				ErrorLogger.Error(fmt.Sprintf("Unable to parse time \"%s\" of eventid \"%v\"", event.NotBefore, event.EventId), err)
				eventValue = 0
			}
		}

		if len(event.Resources) >= 1 {
			for _, resource := range event.Resources {
				scheduledEvent.With(
					prometheus.Labels{
						"eventID": event.EventId,
						"eventType": event.EventType,
						"resourceType": event.ResourceType,
						"resource": resource,
						"eventStatus": event.EventStatus,
						"notBefore": event.NotBefore,
					}).Set(eventValue)
			}
		} else {
			scheduledEvent.With(
				prometheus.Labels{
					"eventID": event.EventId,
					"eventType": event.EventType,
					"resourceType": event.ResourceType,
					"resource": "",
					"eventStatus": event.EventStatus,
					"notBefore": event.NotBefore,
				}).Set(eventValue)
		}
	}

	scheduledEventDocumentIncarnation.With(prometheus.Labels{}).Set(float64(scheduledEvents.DocumentIncarnation))

	Logger.Verbose("Fetched %v Azure ScheduledEvents",len(scheduledEvents.Events))
}

func fetchApiUrl() (*AzureScheduledEventResponse, error) {
	ret := &AzureScheduledEventResponse{}

	req, err := http.NewRequest("GET", opts.ApiUrl, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Metadata", "true")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	err = json.NewDecoder(resp.Body).Decode(&ret)
	if err != nil {
		return nil, err
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

package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	vkit "cloud.google.com/go/firestore/apiv1"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/pgollangi/fireql"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

// Make sure Datasource implements required interfaces. This is important to do
// since otherwise we will only get a not implemented error response from plugin in
// runtime. In this example datasource instance implements backend.QueryDataHandler,
// backend.CheckHealthHandler interfaces. Plugin should not implement all these
// interfaces- only those which are required for a particular task.
var (
	_ backend.QueryDataHandler      = (*Datasource)(nil)
	_ backend.CheckHealthHandler    = (*Datasource)(nil)
	_ instancemgmt.InstanceDisposer = (*Datasource)(nil)
)

// NewDatasource creates a new datasource instance.
func NewDatasource(ctx context.Context, settings backend.DataSourceInstanceSettings) (instancemgmt.Instance, error) {
	return &Datasource{}, nil
}

// Datasource is an example datasource which can respond to data queries, reports
// its health and has streaming skills.
type Datasource struct{}

// Dispose here tells plugin SDK that plugin wants to clean up resources when a new instance
// created. As soon as datasource settings change detected by SDK old datasource instance will
// be disposed and a new one will be created using NewSampleDatasource factory function.
func (d *Datasource) Dispose() {
	// Clean up datasource instance resources.
}

// QueryData handles multiple queries and returns multiple responses.
// req contains the queries []DataQuery (where each query contains RefID as a unique identifier).
// The QueryDataResponse contains a map of RefID to the response for each query, and each response
// contains Frames ([]*Frame).
func (d *Datasource) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
	// when logging at a non-Debug level, make sure you don't include sensitive information in the message
	// (like the *backend.QueryDataRequest)
	log.DefaultLogger.Debug("QueryData called", "numQueries", len(req.Queries))

	// create response struct
	response := backend.NewQueryDataResponse()

	// loop over queries and execute them individually.
	for _, q := range req.Queries {
		res := d.query(ctx, req.PluginContext, q)

		// save the response in a hashmap
		// based on with RefID as identifier
		response.Responses[q.RefID] = res
	}

	return response, nil
}

type FirestoreQuery struct {
	Query         string `json:"query"`
	TimeField     string `json:"timeField,omitempty"`
}

type FirestoreSettings struct {
	ProjectId string
}

func (d *Datasource) query(ctx context.Context, pCtx backend.PluginContext, query backend.DataQuery) (response backend.DataResponse) {
	defer func() {
		if err := recover(); err != nil {
			log.DefaultLogger.Error("panic occurred ", err)
			response = backend.ErrDataResponse(backend.StatusInternal, "internal server error")
		}
	}()
	response = d.queryInternal(ctx, pCtx, query)
	return response
}


func (d *Datasource) queryInternal(ctx context.Context, pCtx backend.PluginContext, query backend.DataQuery) backend.DataResponse {
	var response backend.DataResponse

	// Unmarshal the JSON into our queryModel.
	var qm FirestoreQuery
	err := json.Unmarshal(query.JSON, &qm)
	if err != nil {
		return backend.ErrDataResponse(backend.StatusBadRequest, "json unmarshal: "+err.Error())
	}
	log.DefaultLogger.Debug("FirestoreQuery: ", qm)

	var settings FirestoreSettings
	err = json.Unmarshal(pCtx.DataSourceInstanceSettings.JSONData, &settings)
	if err != nil {
		log.DefaultLogger.Error("Error parsing settings ", err)
		return backend.ErrDataResponse(backend.StatusBadRequest, "ProjectID: "+err.Error())
	}

	if len(settings.ProjectId) == 0 {
		return backend.ErrDataResponse(backend.StatusBadRequest, "ProjectID is required")
	}

	var options []fireql.Option
	if pCtx.DataSourceInstanceSettings.DecryptedSecureJSONData["serviceAccount"] != "" {
		options = append(options, fireql.OptionServiceAccount(pCtx.DataSourceInstanceSettings.DecryptedSecureJSONData["serviceAccount"]))
	}

	fQuery, err := fireql.New(settings.ProjectId, options...)
	if err != nil {
		return backend.ErrDataResponse(backend.StatusBadRequest, "fireql.NewFireQL: "+err.Error())
	}

	log.DefaultLogger.Info("Created fireql.NewFireQLWithServiceAccountJSON")

	if len(qm.Query) > 0 {
		// Start with the original query
		finalQuery := qm.Query

		// Check if query contains Grafana global variables OR GROUP BY - if so, use native SDK
		hasGrafanaVars := containsGrafanaVariables(qm.Query)
		hasGroupBy := containsGroupBy(qm.Query)

		// TEMPORARY DEBUG: Add route info to response if it's a test
		routeInfo := fmt.Sprintf("hasGrafanaVars=%v,hasGroupBy=%v", hasGrafanaVars, hasGroupBy)
		log.DefaultLogger.Info("DEBUG-ROUTE", "routeInfo", routeInfo)

		if (hasGrafanaVars && !query.TimeRange.From.IsZero() && !query.TimeRange.To.IsZero()) || hasGroupBy {
			log.DefaultLogger.Info("ROUTING TO NATIVE SDK", "query", qm.Query, "hasGrafanaVars", hasGrafanaVars, "hasGroupBy", hasGroupBy, "timeFrom", query.TimeRange.From, "timeTo", query.TimeRange.To)
			return d.executeWithNativeSDKForVariables(ctx, pCtx, qm, query.TimeRange)
		}

		log.DefaultLogger.Info("ROUTING TO FIREQL", "query", qm.Query, "hasGrafanaVars", hasGrafanaVars, "hasGroupBy", hasGroupBy)

		// For queries without variables, continue with FireQL
		finalQuery = qm.Query

		// Time filtering is now manual using $__from and $__to variables in the query
		// No automatic filtering to avoid index requirements for complex queries

		// No automatic limit - user must specify LIMIT in query if needed

		log.DefaultLogger.Info("Executing query", finalQuery)

		// Execute query directly
		result, err := fQuery.Execute(finalQuery)
		if err != nil {
			log.DefaultLogger.Error("Query execution failed", "error", err.Error(), "query", finalQuery)
			return backend.ErrDataResponse(backend.StatusBadRequest, "fireql.Execute: "+err.Error())
		}

		// Safely log query results
		if result == nil {
			log.DefaultLogger.Error("Query returned nil result")
			return backend.ErrDataResponse(backend.StatusInternal, "Query returned nil result")
		}

		log.DefaultLogger.Info("Query executed successfully", "columns", len(result.Columns), "records", len(result.Records))
		if len(result.Records) == 0 {
			log.DefaultLogger.Warn("No records returned - check timestamp format compatibility")
		}

		// Protect against excessive memory usage
		if len(result.Records) > 10000 {
			log.DefaultLogger.Warn("Large result set detected, truncating to prevent memory issues", "originalSize", len(result.Records), "truncatedTo", 10000)
			result.Records = result.Records[:10000]
		}

		fieldValues := make(map[string]interface{})

		for idx, column := range result.Columns {
			var values interface{}
			if len(result.Records) > 0 {
				for recordIdx, record := range result.Records {
					if record == nil {
						log.DefaultLogger.Warn("Skipping nil record", "recordIndex", recordIdx)
						continue
					}
					if idx >= len(record) {
						log.DefaultLogger.Warn("Column index out of bounds for record", "columnIndex", idx, "recordLength", len(record), "recordIndex", recordIdx)
						continue
					}
					val := record[idx]
					if val == nil {
						continue // Skip nil values
					}
					switch val.(type) {
					case bool:
						if values == nil {
							values = []bool{}
						}
						values = append(values.([]bool), val.(bool))
						break
					case int:
						if values == nil {
							values = []int32{}
						}
						values = append(values.([]int32), int32(val.(int)))
						break
					case int32:
						if values == nil {
							values = []int32{}
						}
						values = append(values.([]int32), val.(int32))
						break
					case int64:
						if values == nil {
							values = []int64{}
						}
						values = append(values.([]int64), val.(int64))
						break
					case float64:
						if values == nil {
							values = []float64{}
						}
						values = append(values.([]float64), val.(float64))
						break
					case time.Time:
						if values == nil {
							values = []time.Time{}
						}
						values = append(values.([]time.Time), val.(time.Time))
						break
					case map[string]interface{}, []map[string]interface{}, []interface{}:
						if values == nil {
							values = []json.RawMessage{}
						}
						jsonVal, err := json.Marshal(val)
						if err != nil {
							return backend.ErrDataResponse(backend.StatusBadRequest, "json.Marshal : "+column+err.Error())
						} else {
							values = append(values.([]json.RawMessage), json.RawMessage(jsonVal))
						}
						break
					default:
						if values == nil {
							values = []string{}
						}
						values = append(values.([]string), fmt.Sprintf("%v", val))
					}
				}
			} else {
				values = []string{}
			}
			fieldValues[column] = values
		}

		// create data frame response.
		frame := data.NewFrame("response")
		for _, column := range result.Columns {
			// Add debug info to show this is using FireQL path
			debugColumn := column + "_USING_FIREQL"
			frame.Fields = append(frame.Fields,
				data.NewField(debugColumn, nil, fieldValues[column]),
			)
		}
		// add the frames to the response.
		response.Frames = append(response.Frames, frame)
	}

	return response
}

func newFirestoreClient(ctx context.Context, pCtx backend.PluginContext) (*firestore.Client, error) {
	var settings FirestoreSettings
	err := json.Unmarshal(pCtx.DataSourceInstanceSettings.JSONData, &settings)
	if err != nil {
		log.DefaultLogger.Error("Error parsing settings ", err)
		return nil, fmt.Errorf("ProjectID: %v", err)
	}

	if len(settings.ProjectId) == 0 {
		return nil, errors.New("project Id is required")
	}

	var options []option.ClientOption
	serviceAccount := pCtx.DataSourceInstanceSettings.DecryptedSecureJSONData["serviceAccount"]

	if len(serviceAccount) > 0 {
		if !json.Valid([]byte(serviceAccount)) {
			return nil, errors.New("invalid service account, it is expected to be a JSON")
		}
		creds, err := google.CredentialsFromJSON(ctx, []byte(serviceAccount),
			vkit.DefaultAuthScopes()...,
		)
		if err != nil {
			log.DefaultLogger.Error("google.CredentialsFromJSON ", err)
			return nil, fmt.Errorf("ServiceAccount: %v", err)
		}
		options = append(options, option.WithCredentials(creds))
	}
	client, err := firestore.NewClient(ctx, settings.ProjectId, options...)
	if err != nil {
		log.DefaultLogger.Error("firestore.NewClient ", err)
		return nil, fmt.Errorf("firestore.NewClient: %v", err)
	}
	return client, nil
}

// containsGrafanaVariables checks if the query contains Grafana global time variables
func containsGrafanaVariables(query string) bool {
	return strings.Contains(query, "$__from") || strings.Contains(query, "$__to")
}

// containsGroupBy checks if the query contains GROUP BY clause
func containsGroupBy(query string) bool {
	return strings.Contains(strings.ToLower(query), "group by")
}

// replaceGrafanaVariables replaces Grafana global variables with actual timestamp values
func replaceGrafanaVariables(query string, timeRange backend.TimeRange) string {
	// Based on testing, we discovered that Firestore/FireQL has issues with timestamp comparisons
	// Let's try multiple formats and log them for debugging

	// Format 1: Unix milliseconds (original approach)
	fromMillis := timeRange.From.UnixMilli()
	toMillis := timeRange.To.UnixMilli()

	// Format 2: RFC3339 timestamps
	fromRFC := timeRange.From.Format(time.RFC3339)
	toRFC := timeRange.To.Format(time.RFC3339)

	// Format 3: Firestore timestamp format (ISO 8601 with nanoseconds)
	fromISO := timeRange.From.Format("2006-01-02T15:04:05.999999999Z")
	toISO := timeRange.To.Format("2006-01-02T15:04:05.999999999Z")

	// Based on testing, direct numeric comparison with Unix milliseconds should work
	// The data inspect showed timestamps as Unix milliseconds: 1757789690410, 1758187471102, etc.
	result := strings.ReplaceAll(query, "$__from", fmt.Sprintf("%d", fromMillis))
	result = strings.ReplaceAll(result, "$__to", fmt.Sprintf("%d", toMillis))

	log.DefaultLogger.Info("Replaced Grafana variables with Unix milliseconds",
		"fromMillis", fromMillis,
		"toMillis", toMillis,
		"fromRFC", fromRFC,
		"toRFC", toRFC,
		"fromISO", fromISO,
		"toISO", toISO,
		"finalQuery", result)

	return result
}

// addTimeRangeFilter adds a time range filter to the SQL query
func addTimeRangeFilter(query, timeField string, timeRange backend.TimeRange) string {
	// Convert to Unix timestamp in MILLISECONDS (not seconds)
	// Firestore stores timestamps as Unix milliseconds: 1758183895512
	fromMillis := timeRange.From.UnixMilli()
	toMillis := timeRange.To.UnixMilli()

	log.DefaultLogger.Info("Time filter values",
		"field", timeField,
		"fromMillis", fromMillis,
		"toMillis", toMillis)

	// Use numeric comparison matching the inspect data format (1758183895512)
	// Firestore timestamps are stored as Unix milliseconds
	timeFilter := fmt.Sprintf("%s >= %d and %s <= %d", timeField, fromMillis, timeField, toMillis)

	log.DefaultLogger.Info("Using numeric Unix milliseconds for timestamp filtering",
		"timeField", timeField,
		"fromMillis", fromMillis,
		"toMillis", toMillis,
		"generatedFilter", timeFilter)

	log.DefaultLogger.Info("Using Unix milliseconds for Firestore timestamp field", "filter", timeFilter)

	// Check if the query already has a WHERE clause
	queryLower := strings.ToLower(query)
	if strings.Contains(queryLower, " where ") {
		// Add to existing WHERE clause with AND
		return query + " and " + timeFilter
	} else {
		// Add new WHERE clause
		// Find the position to insert WHERE clause (before ORDER BY, LIMIT, etc.)
		insertPos := len(query)

		// Look for ORDER BY, LIMIT, or GROUP BY clauses and insert before them
		keywords := []string{" order by ", " limit ", " group by "}
		for _, keyword := range keywords {
			if pos := strings.Index(queryLower, keyword); pos != -1 && pos < insertPos {
				insertPos = pos
			}
		}

		return query[:insertPos] + " where " + timeFilter + query[insertPos:]
	}
}

// CheckHealth handles health checks sent from Grafana to the plugin.
// The main use case for these health checks is the test button on the
// datasource configuration page which allows users to verify that
// a datasource is working as expected.
func (d *Datasource) CheckHealth(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	// when logging at a non-Debug level, make sure you don't include sensitive information in the message
	// (like the *backend.QueryDataRequest)
	log.DefaultLogger.Debug("CheckHealth called")

	var status = backend.HealthStatusOk
	var message = "Data source is working"

	client, healthErr := newFirestoreClient(ctx, req.PluginContext)

	if healthErr == nil {
		defer client.Close()
		collections := client.Collections(ctx)
		collection, err := collections.Next()
		if err == nil || errors.Is(err, iterator.Done) {
			log.DefaultLogger.Debug("First collections: ", collection.ID)
		} else {
			log.DefaultLogger.Error("client.Collections ", err)
			healthErr = fmt.Errorf("firestore.Collections: %v", err)
		}
	}

	if healthErr != nil {
		status = backend.HealthStatusError
		message = healthErr.Error()
	}

	return &backend.CheckHealthResult{
		Status:  status,
		Message: message,
	}, nil
}


// executeWithTimeout executes a query with timeout protection
func executeWithTimeout(ctx context.Context, fQuery *fireql.FireQL, query string) (interface{}, error) {
	resultChan := make(chan interface{}, 1)
	errorChan := make(chan error, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.DefaultLogger.Error("Panic in query execution", "panic", r)
				errorChan <- fmt.Errorf("query execution panic: %v", r)
			}
		}()

		result, err := fQuery.Execute(query)
		if err != nil {
			errorChan <- err
		} else {
			resultChan <- result
		}
	}()

	select {
	case result := <-resultChan:
		return result, nil
	case err := <-errorChan:
		return nil, err
	case <-ctx.Done():
		return nil, fmt.Errorf("query execution timeout after 30 seconds")
	}
}

// isSimpleQuery checks if the query is simple enough to use native Firestore SDK
func isSimpleQuery(query string) bool {
	queryLower := strings.ToLower(strings.TrimSpace(query))

	// Match patterns like: "SELECT * FROM collection" or "SELECT field1, field2 FROM collection"
	// Simple queries that can be converted to native Firestore queries
	return strings.HasPrefix(queryLower, "select") &&
		   strings.Contains(queryLower, "from") &&
		   !strings.Contains(queryLower, "where") &&
		   !strings.Contains(queryLower, "join") &&
		   !strings.Contains(queryLower, "group by") &&
		   !strings.Contains(queryLower, "having")
}

// executeWithNativeSDK executes simple queries using native Firestore SDK with timestamp filtering
func (d *Datasource) executeWithNativeSDK(ctx context.Context, pCtx backend.PluginContext, qm FirestoreQuery, timeRange backend.TimeRange) backend.DataResponse {
	log.DefaultLogger.Info("Executing with native Firestore SDK", "query", qm.Query, "timeField", qm.TimeField)

	// Create Firestore client
	client, err := newFirestoreClient(ctx, pCtx)
	if err != nil {
		log.DefaultLogger.Error("Failed to create Firestore client", "error", err)
		return backend.ErrDataResponse(backend.StatusBadRequest, "Firestore client: "+err.Error())
	}
	defer client.Close()

	// Parse collection name from query
	collectionName := extractCollectionName(qm.Query)
	if collectionName == "" {
		log.DefaultLogger.Error("Could not extract collection name from query", "query", qm.Query)
		return backend.ErrDataResponse(backend.StatusBadRequest, "Could not parse collection name")
	}

	log.DefaultLogger.Info("Using native SDK for collection", "collection", collectionName, "timeField", qm.TimeField)

	// Build native Firestore query with timestamp filtering
	firestoreQuery := client.Collection(collectionName).
		Where(qm.TimeField, ">=", timeRange.From).
		Where(qm.TimeField, "<=", timeRange.To).
		OrderBy(qm.TimeField, firestore.Desc)

	// Execute query
	docs, err := firestoreQuery.Documents(ctx).GetAll()
	if err != nil {
		log.DefaultLogger.Error("Native Firestore query failed", "error", err)
		return backend.ErrDataResponse(backend.StatusBadRequest, "Native query: "+err.Error())
	}

	log.DefaultLogger.Info("Native query executed successfully", "documents", len(docs))

	// Convert results to Grafana format
	return d.convertFirestoreDocsToResponse(docs, qm)
}

// extractCollectionName extracts collection name from SQL-like query
func extractCollectionName(query string) string {
	queryLower := strings.ToLower(strings.TrimSpace(query))

	// Find "FROM collection_name"
	fromIndex := strings.Index(queryLower, "from ")
	if fromIndex == -1 {
		return ""
	}

	// Extract everything after "FROM "
	afterFrom := strings.TrimSpace(query[fromIndex+5:])

	// Get first word (collection name)
	parts := strings.Fields(afterFrom)
	if len(parts) == 0 {
		return ""
	}

	return parts[0]
}

// convertFirestoreDocsToResponse converts Firestore documents to Grafana response format
func (d *Datasource) convertFirestoreDocsToResponse(docs []*firestore.DocumentSnapshot, qm FirestoreQuery) backend.DataResponse {
	var response backend.DataResponse

	if len(docs) == 0 {
		// Return empty frame
		frame := data.NewFrame("response")
		frame.Fields = append(frame.Fields, data.NewField(qm.TimeField, nil, []time.Time{}))
		response.Frames = append(response.Frames, frame)
		return response
	}

	// Extract all unique field names from documents
	fieldMap := make(map[string][]interface{})

	for _, doc := range docs {
		docData := doc.Data()
		for fieldName, value := range docData {
			if fieldMap[fieldName] == nil {
				fieldMap[fieldName] = make([]interface{}, 0, len(docs))
			}

			// Convert timestamp to time.Time for Grafana
			if fieldName == qm.TimeField {
				if ts, ok := value.(time.Time); ok {
					fieldMap[fieldName] = append(fieldMap[fieldName], ts)
				} else {
					fieldMap[fieldName] = append(fieldMap[fieldName], value)
				}
			} else {
				fieldMap[fieldName] = append(fieldMap[fieldName], value)
			}
		}
	}

	// Create data frame
	frame := data.NewFrame("response")

	// Add fields to frame
	for fieldName, values := range fieldMap {
		// Ensure all fields have the same length by padding with nil
		for len(values) < len(docs) {
			values = append(values, nil)
		}

		if fieldName == qm.TimeField {
			// Time field
			timeValues := make([]time.Time, 0, len(values))
			for _, v := range values {
				if ts, ok := v.(time.Time); ok {
					timeValues = append(timeValues, ts)
				} else {
					timeValues = append(timeValues, time.Time{})
				}
			}
			frame.Fields = append(frame.Fields, data.NewField(fieldName, nil, timeValues))
		} else {
			// Other fields - convert to strings for simplicity
			stringValues := make([]string, 0, len(values))
			for _, v := range values {
				if v != nil {
					stringValues = append(stringValues, fmt.Sprintf("%v", v))
				} else {
					stringValues = append(stringValues, "")
				}
			}
			frame.Fields = append(frame.Fields, data.NewField(fieldName, nil, stringValues))
		}
	}

	response.Frames = append(response.Frames, frame)
	return response
}

// executeWithNativeSDKForVariables handles queries with $__from/$__to variables using native Firestore SDK
func (d *Datasource) executeWithNativeSDKForVariables(ctx context.Context, pCtx backend.PluginContext, qm FirestoreQuery, timeRange backend.TimeRange) backend.DataResponse {
	log.DefaultLogger.Info("Executing query with Grafana variables using native SDK", "query", qm.Query)

	// Create Firestore client
	client, err := newFirestoreClient(ctx, pCtx)
	if err != nil {
		log.DefaultLogger.Error("Failed to create Firestore client", "error", err)
		return backend.ErrDataResponse(backend.StatusBadRequest, "Firestore client: "+err.Error())
	}
	defer client.Close()

	// Parse the SQL query to extract collection, fields, and additional filters
	queryInfo, err := parseSQLQueryWithVariables(qm.Query)
	if err != nil {
		log.DefaultLogger.Error("Failed to parse SQL query", "error", err, "query", qm.Query)
		return backend.ErrDataResponse(backend.StatusBadRequest, "Query parsing: "+err.Error())
	}

	log.DefaultLogger.Info("Query parsed successfully", "collection", queryInfo.Collection, "groupByFields", queryInfo.GroupByFields, "aggregateFields", queryInfo.AggregateFields)
	log.DefaultLogger.Info("Parsed query info", "collection", queryInfo.Collection, "timeField", queryInfo.TimeField, "fields", queryInfo.Fields, "additionalFilters", queryInfo.AdditionalFilters)

	// Build native Firestore query
	var firestoreQuery firestore.Query = client.Collection(queryInfo.Collection).Query

	// Add time range filter using the detected time field
	if queryInfo.TimeField != "" {
		firestoreQuery = firestoreQuery.Where(queryInfo.TimeField, ">=", timeRange.From)
		firestoreQuery = firestoreQuery.Where(queryInfo.TimeField, "<=", timeRange.To)
		log.DefaultLogger.Info("Added time range filter", "field", queryInfo.TimeField, "from", timeRange.From, "to", timeRange.To)
	}

	// Add additional WHERE filters (non-time filters)
	// Skip ALL Firestore WHERE filters to avoid index requirements - we'll filter manually in GROUP BY processing
	for _, filter := range queryInfo.AdditionalFilters {
		// Apply all filters manually to avoid index requirements
		log.DefaultLogger.Info("Skipping Firestore filter (will apply manually to avoid index requirements)", "field", filter.Field, "operator", filter.Operator, "value", filter.Value)
	}

	// Add ordering if specified (but not for GROUP BY queries - ordering is handled post-aggregation)
	if queryInfo.OrderField != "" && len(queryInfo.GroupByFields) == 0 && len(queryInfo.AggregateFields) == 0 {
		direction := firestore.Asc
		if queryInfo.OrderDirection == "DESC" {
			direction = firestore.Desc
		}
		firestoreQuery = firestoreQuery.OrderBy(queryInfo.OrderField, direction)
		log.DefaultLogger.Info("Added ordering", "field", queryInfo.OrderField, "direction", queryInfo.OrderDirection)
	} else if queryInfo.OrderField != "" && (len(queryInfo.GroupByFields) > 0 || len(queryInfo.AggregateFields) > 0) {
		log.DefaultLogger.Info("Skipping Firestore ORDER BY for GROUP BY query - will be handled post-aggregation", "field", queryInfo.OrderField)
	}

	// Add limit
	if queryInfo.Limit > 0 {
		firestoreQuery = firestoreQuery.Limit(queryInfo.Limit)
	}

	// Execute query
	docs, err := firestoreQuery.Documents(ctx).GetAll()
	if err != nil {
		log.DefaultLogger.Error("Native Firestore query with variables failed", "error", err)
		return backend.ErrDataResponse(backend.StatusBadRequest, "Native query: "+err.Error())
	}

	log.DefaultLogger.Info("Native query with variables executed successfully", "documents", len(docs))

	// Apply manual filtering for additional WHERE conditions (both GROUP BY and simple queries)
	if len(queryInfo.AdditionalFilters) > 0 {
		log.DefaultLogger.Info("APPLYING MANUAL FILTERING FOR ADDITIONAL WHERE CONDITIONS", "totalDocs", len(docs), "additionalFilters", len(queryInfo.AdditionalFilters))
		docs = d.applyManualFiltering(docs, queryInfo.AdditionalFilters)
		log.DefaultLogger.Info("MANUAL FILTERING COMPLETE", "remainingDocs", len(docs))
	}

	// Check if this is a GROUP BY query that needs in-memory aggregation
	if len(queryInfo.GroupByFields) > 0 || len(queryInfo.AggregateFields) > 0 {
		log.DefaultLogger.Info("PROCESSING GROUP BY WITH NEW FUNCTION", "groupFields", queryInfo.GroupByFields, "aggregateFields", queryInfo.AggregateFields, "docs", len(docs))
		for i, field := range queryInfo.AggregateFields {
			log.DefaultLogger.Info("Aggregate field details", "index", i, "function", field.Function, "field", field.Field, "alias", field.Alias)
		}
		return d.processGroupByQueryWithOrdering(docs, queryInfo)
	}

	// Convert results to Grafana format
	return d.convertFirestoreDocsToResponseWithFields(docs, queryInfo)
}

// QueryInfo holds parsed SQL query information
type QueryInfo struct {
	Collection        string
	Fields           []string
	TimeField        string
	AdditionalFilters []FilterInfo
	OrderField       string
	OrderDirection   string
	Limit            int
	GroupByFields    []string
	AggregateFields  []AggregateInfo
}

// AggregateInfo holds information about aggregate functions
type AggregateInfo struct {
	Function string // COUNT, SUM, AVG, etc.
	Field    string // field to aggregate on, "*" for COUNT(*)
	Alias    string // alias name (e.g., "total" in COUNT(*) as total)
}

// FilterInfo holds WHERE clause filter information
type FilterInfo struct {
	Field    string
	Operator string
	Value    interface{}
}

// parseSQLQueryWithVariables parses SQL queries that contain $__from/$__to variables
func parseSQLQueryWithVariables(query string) (*QueryInfo, error) {
	queryLower := strings.ToLower(strings.TrimSpace(query))
	queryOriginal := strings.TrimSpace(query)

	log.DefaultLogger.Error("STARTING PARSE", "query", query)

	info := &QueryInfo{
		Fields: []string{},
		AdditionalFilters: []FilterInfo{},
		GroupByFields: []string{},
		AggregateFields: []AggregateInfo{},
		Limit: 0,
	}

	// Extract SELECT fields
	selectIdx := strings.Index(queryLower, "select ")
	fromIdx := strings.Index(queryLower, " from ")
	if selectIdx == -1 || fromIdx == -1 {
		return nil, fmt.Errorf("invalid SQL: missing SELECT or FROM")
	}

	// Parse fields using the new aggregate parser
	fieldsStr := strings.TrimSpace(queryOriginal[selectIdx+7 : fromIdx])
	log.DefaultLogger.Error("ABOUT TO PARSE FIELDS", "fieldsStr", fieldsStr)
	parseAggregateFields(fieldsStr, info)
	log.DefaultLogger.Error("AFTER PARSING FIELDS", "regularFields", info.Fields, "aggregateFields", info.AggregateFields)

	// Extract collection name
	whereIdx := strings.Index(queryLower, " where ")
	groupIdx := findGroupByIndex(queryLower)
	orderIdx := strings.Index(queryLower, " order by ")
	limitIdx := findLimitIndex(queryLower)

	log.DefaultLogger.Info("SQL PARSING INDEXES", "whereIdx", whereIdx, "groupIdx", groupIdx, "orderIdx", orderIdx, "limitIdx", limitIdx)
	log.DefaultLogger.Info("QUERY FOR PARSING", "originalQuery", queryOriginal)

	endIdx := len(queryOriginal)
	if whereIdx != -1 {
		endIdx = whereIdx
	}
	if groupIdx != -1 && groupIdx < endIdx {
		endIdx = groupIdx
	}
	if orderIdx != -1 && orderIdx < endIdx {
		endIdx = orderIdx
	}
	if limitIdx != -1 && limitIdx < endIdx {
		endIdx = limitIdx
	}

	collectionStr := strings.TrimSpace(queryOriginal[fromIdx+6 : endIdx])
	info.Collection = collectionStr

	// Parse WHERE clause to find time field and additional filters
	if whereIdx != -1 {
		whereEndIdx := len(queryOriginal)
		if groupIdx != -1 && groupIdx > whereIdx {
			whereEndIdx = groupIdx
		}
		if orderIdx != -1 && orderIdx > whereIdx {
			whereEndIdx = orderIdx
		}
		if limitIdx != -1 && limitIdx > whereIdx {
			whereEndIdx = limitIdx
		}

		whereClause := strings.TrimSpace(queryOriginal[whereIdx+7 : whereEndIdx])
		log.DefaultLogger.Info("PARSING WHERE CLAUSE", "whereClause", whereClause)
		parseWhereClause(whereClause, info)
		log.DefaultLogger.Info("PARSED FILTERS", "additionalFilters", len(info.AdditionalFilters), "timeField", info.TimeField)
		for i, filter := range info.AdditionalFilters {
			log.DefaultLogger.Info("FILTER DETAILS", "index", i, "field", filter.Field, "operator", filter.Operator, "value", filter.Value)
		}
	}

	// Parse GROUP BY
	if groupIdx != -1 {
		groupStartIdx := groupIdx + 10 // Skip "GROUP BY "
		groupEndIdx := len(queryOriginal)

		// Find the closest following clause to determine where GROUP BY ends
		// Priority: ORDER BY > LIMIT (ORDER BY should come before LIMIT)
		if orderIdx != -1 && orderIdx > groupIdx {
			groupEndIdx = orderIdx
		} else if limitIdx != -1 && limitIdx > groupIdx {
			groupEndIdx = limitIdx
		}

		log.DefaultLogger.Info("GROUP BY PARSING", "groupIdx", groupIdx, "groupStartIdx", groupStartIdx, "groupEndIdx", groupEndIdx, "orderIdx", orderIdx, "limitIdx", limitIdx)
		groupClause := strings.TrimSpace(queryOriginal[groupStartIdx : groupEndIdx])
		log.DefaultLogger.Info("GROUP BY CLAUSE EXTRACTED", "groupClause", groupClause)
		parseGroupBy(groupClause, info)
	}

	// Parse ORDER BY
	if orderIdx != -1 {
		orderEndIdx := len(queryOriginal)
		if limitIdx != -1 && limitIdx > orderIdx {
			orderEndIdx = limitIdx
		}
		orderClause := strings.TrimSpace(queryOriginal[orderIdx+10 : orderEndIdx])
		parseOrderBy(orderClause, info)
	}

	// Parse LIMIT
	if limitIdx != -1 {
		limitStr := strings.TrimSpace(queryOriginal[limitIdx+7:])
		if limit, err := parseLimit(limitStr); err == nil {
			info.Limit = limit
		}
	}

	log.DefaultLogger.Info("PARSE COMPLETE", "groupByFields", info.GroupByFields, "aggregateFields", info.AggregateFields, "regularFields", info.Fields)
	return info, nil
}

// parseWhereClause parses WHERE conditions to identify time fields and other filters
func parseWhereClause(whereClause string, info *QueryInfo) {
	// Look for $__from and $__to variables to identify the time field
	if strings.Contains(whereClause, "$__from") || strings.Contains(whereClause, "$__to") {
		// Extract time field name from patterns like "fieldName >= $__from"
		parts := strings.Fields(whereClause)
		for i, part := range parts {
			if (part == ">=" || part == "<=" || part == ">" || part == "<") && i > 0 {
				if i+1 < len(parts) && (strings.Contains(parts[i+1], "$__from") || strings.Contains(parts[i+1], "$__to")) {
					info.TimeField = parts[i-1]
					break
				}
			}
		}
	}

	// Parse other WHERE conditions (non-time filters)
	// Simple parsing for equality conditions like: field = 'value' or field == "value"
	conditions := strings.Split(whereClause, " AND ")
	log.DefaultLogger.Info("PARSING WHERE CONDITIONS", "whereClause", whereClause, "splitConditions", conditions)
	for i, condition := range conditions {
		condition = strings.TrimSpace(condition)
		log.DefaultLogger.Info("PROCESSING CONDITION", "index", i, "condition", condition)
		if !strings.Contains(condition, "$__from") && !strings.Contains(condition, "$__to") {
			// Parse condition like "msisdn = '633525465'" or "clientData.BrandCliente == \"yoigo\"" or "msisdn==\"681021597\""
			if strings.Contains(condition, "==") {
				// Handle both "field == value" and "field==\"value\""
				var parts []string
				if strings.Contains(condition, " == ") {
					parts = strings.SplitN(condition, " == ", 2)
				} else {
					parts = strings.SplitN(condition, "==", 2)
				}
				log.DefaultLogger.Info("FOUND == OPERATOR", "parts", parts)
				if len(parts) == 2 {
					field := strings.TrimSpace(parts[0])
					value := strings.Trim(strings.TrimSpace(parts[1]), "'\"")
					log.DefaultLogger.Info("ADDING FILTER WITH ==", "field", field, "value", value)
					info.AdditionalFilters = append(info.AdditionalFilters, FilterInfo{
						Field:    field,
						Operator: "==",
						Value:    value,
					})
				}
			} else if strings.Contains(condition, "=") {
				// Handle both "field = value" and "field=value"
				var parts []string
				if strings.Contains(condition, " = ") {
					parts = strings.SplitN(condition, " = ", 2)
				} else {
					parts = strings.SplitN(condition, "=", 2)
				}
				log.DefaultLogger.Info("FOUND = OPERATOR", "parts", parts)
				if len(parts) == 2 {
					field := strings.TrimSpace(parts[0])
					value := strings.Trim(strings.TrimSpace(parts[1]), "'\"")
					log.DefaultLogger.Info("ADDING FILTER WITH =", "field", field, "value", value)
					info.AdditionalFilters = append(info.AdditionalFilters, FilterInfo{
						Field:    field,
						Operator: "==",
						Value:    value,
					})
				}
			} else {
				log.DefaultLogger.Info("NO OPERATOR FOUND IN CONDITION", "condition", condition)
			}
		} else {
			log.DefaultLogger.Info("SKIPPING TIME CONDITION", "condition", condition)
		}
	}
}

// parseGroupBy parses GROUP BY clause
func parseGroupBy(groupClause string, info *QueryInfo) {
	fields := strings.Split(groupClause, ",")
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			// Clean backticks from field names
			cleanField := cleanBackticks(field)
			info.GroupByFields = append(info.GroupByFields, cleanField)
		}
	}
}

// cleanBackticks removes backticks from field names
func cleanBackticks(field string) string {
	return strings.Trim(strings.TrimSpace(field), "`")
}

// parseAggregateFields parses SELECT fields to identify aggregate functions
func parseAggregateFields(fieldsStr string, info *QueryInfo) {
	fields := strings.Split(fieldsStr, ",")
	info.Fields = []string{}
	info.AggregateFields = []AggregateInfo{}

	log.DefaultLogger.Error("PARSING FIELDS", "fieldsStr", fieldsStr, "splitFields", fields)

	for _, field := range fields {
		field = strings.TrimSpace(field)
		log.DefaultLogger.Info("PROCESSING FIELD", "field", field)

		if field == "*" {
			info.Fields = append(info.Fields, "*")
			continue
		}

		// Check for aggregate functions like COUNT(*), SUM(field), AVG(field)
		upperField := strings.ToUpper(field)
		log.DefaultLogger.Info("CHECKING AGGREGATE", "field", field, "upperField", upperField)

		if strings.Contains(upperField, "COUNT(") || strings.Contains(upperField, "SUM(") ||
		   strings.Contains(upperField, "AVG(") || strings.Contains(upperField, "MIN(") ||
		   strings.Contains(upperField, "MAX(") {

			log.DefaultLogger.Info("DETECTED AGGREGATE FUNCTION", "field", field)

			// Parse aggregate function
			var funcName, fieldName, alias string

			// Extract function name
			if strings.HasPrefix(upperField, "COUNT(") {
				funcName = "COUNT"
			} else if strings.HasPrefix(upperField, "SUM(") {
				funcName = "SUM"
			} else if strings.HasPrefix(upperField, "AVG(") {
				funcName = "AVG"
			} else if strings.HasPrefix(upperField, "MIN(") {
				funcName = "MIN"
			} else if strings.HasPrefix(upperField, "MAX(") {
				funcName = "MAX"
			}

			// Extract field name from function
			start := strings.Index(field, "(")
			end := strings.Index(field, ")")
			if start != -1 && end != -1 && end > start {
				fieldName = strings.TrimSpace(field[start+1:end])
			}

			// Check for alias (AS keyword) - case insensitive search but preserve original case
			upperFieldForParsing := strings.ToUpper(field)
			if strings.Contains(upperFieldForParsing, " AS ") {
				// Find AS position in original field (case-insensitive)
				asPos := strings.Index(upperFieldForParsing, " AS ")
				if asPos != -1 {
					// Extract alias from original field, preserving case
					aliasStart := asPos + 4 // Skip " AS "
					alias = strings.TrimSpace(field[aliasStart:])
				}
			} else {
				// Default alias is the original field
				alias = field
			}

			info.AggregateFields = append(info.AggregateFields, AggregateInfo{
				Function: funcName,
				Field:    fieldName,
				Alias:    alias,
			})
		} else {
			// Regular field (non-aggregate) - clean backticks
			cleanField := cleanBackticks(field)
			log.DefaultLogger.Info("REGULAR FIELD", "field", field, "cleanField", cleanField)
			info.Fields = append(info.Fields, cleanField)
		}
	}
}

// parseOrderBy parses ORDER BY clause
func parseOrderBy(orderClause string, info *QueryInfo) {
	parts := strings.Fields(orderClause)
	if len(parts) >= 1 {
		info.OrderField = parts[0]
		info.OrderDirection = "ASC"
		if len(parts) >= 2 && strings.ToUpper(parts[1]) == "DESC" {
			info.OrderDirection = "DESC"
		}
	}
}

// parseLimit parses LIMIT clause
func parseLimit(limitStr string) (int, error) {
	parts := strings.Fields(limitStr)
	if len(parts) >= 1 {
		return strconv.Atoi(parts[0])
	}
	return 0, fmt.Errorf("invalid limit")
}

// convertFirestoreDocsToResponseWithFields converts docs to Grafana format with specific fields
func (d *Datasource) convertFirestoreDocsToResponseWithFields(docs []*firestore.DocumentSnapshot, queryInfo *QueryInfo) backend.DataResponse {
	var response backend.DataResponse

	if len(docs) == 0 {
		// Return empty frame with requested fields using proper data types
		frame := data.NewFrame("response")
		for _, field := range queryInfo.Fields {
			if field == "*" {
				frame.Fields = append(frame.Fields, data.NewField("no_data", nil, []string{}))
				break
			}
			// Create properly typed empty arrays based on field type
			if field == queryInfo.TimeField {
				// Time field - use empty time.Time array
				frame.Fields = append(frame.Fields, data.NewField(field, nil, []time.Time{}))
			} else {
				// Other fields - use empty string array
				frame.Fields = append(frame.Fields, data.NewField(field, nil, []string{}))
			}
		}
		response.Frames = append(response.Frames, frame)
		return response
	}

	// Collect data for requested fields
	fieldData := make(map[string][]interface{})

	// If SELECT *, get all fields from documents
	if len(queryInfo.Fields) == 1 && queryInfo.Fields[0] == "*" {
		// Get all unique field names
		allFields := make(map[string]bool)
		for _, doc := range docs {
			for fieldName := range doc.Data() {
				allFields[fieldName] = true
			}
		}
		queryInfo.Fields = []string{}
		for fieldName := range allFields {
			queryInfo.Fields = append(queryInfo.Fields, fieldName)
		}
	}

	// Initialize field data arrays
	for _, fieldName := range queryInfo.Fields {
		fieldData[fieldName] = make([]interface{}, 0, len(docs))
	}

	// Extract data from documents
	for i, doc := range docs {
		if doc == nil {
			log.DefaultLogger.Warn("convertFirestoreDocsToResponseWithFields: Skipping nil document", "index", i)
			continue
		}

		docData := doc.Data()
		if docData == nil {
			log.DefaultLogger.Warn("convertFirestoreDocsToResponseWithFields: Skipping document with nil data", "index", i)
			continue
		}

		for _, fieldName := range queryInfo.Fields {
			if value, exists := docData[fieldName]; exists {
				fieldData[fieldName] = append(fieldData[fieldName], value)
			} else {
				fieldData[fieldName] = append(fieldData[fieldName], nil)
			}
		}
	}

	// Create data frame
	frame := data.NewFrame("response")

	for _, fieldName := range queryInfo.Fields {
		values := fieldData[fieldName]

		// Handle different data types
		if fieldName == queryInfo.TimeField {
			// Time field - ensure it's time.Time
			timeValues := make([]time.Time, 0, len(values))
			for _, v := range values {
				if ts, ok := v.(time.Time); ok {
					timeValues = append(timeValues, ts)
				} else {
					timeValues = append(timeValues, time.Time{})
				}
			}
			frame.Fields = append(frame.Fields, data.NewField(fieldName, nil, timeValues))
		} else {
			// Other fields - convert to strings for simplicity
			stringValues := make([]string, 0, len(values))
			for _, v := range values {
				if v != nil {
					stringValues = append(stringValues, fmt.Sprintf("%v", v))
				} else {
					stringValues = append(stringValues, "")
				}
			}
			frame.Fields = append(frame.Fields, data.NewField(fieldName, nil, stringValues))
		}
	}

	response.Frames = append(response.Frames, frame)
	return response
}
// processGroupByQueryWithOrdering handles GROUP BY queries with in-memory aggregation and ORDER BY support
func (d *Datasource) processGroupByQueryWithOrdering(docs []*firestore.DocumentSnapshot, queryInfo *QueryInfo) backend.DataResponse {
	var response backend.DataResponse

	if len(docs) == 0 {
		// Return empty frame with group fields and aggregate fields
		frame := data.NewFrame("response")
		for _, field := range queryInfo.GroupByFields {
			frame.Fields = append(frame.Fields, data.NewField(field, nil, []string{}))
		}
		for _, aggField := range queryInfo.AggregateFields {
			frame.Fields = append(frame.Fields, data.NewField(aggField.Alias, nil, []float64{}))
		}
		response.Frames = append(response.Frames, frame)
		return response
	}

	// Step 1: Apply manual filtering and group documents by group fields
	filteredDocs := d.applyManualFiltering(docs, queryInfo.AdditionalFilters)
	groups := make(map[string][]map[string]interface{})

	for _, doc := range filteredDocs {
		docData := doc.Data()

		// Build group key from group fields
		var keyParts []string
		for _, groupField := range queryInfo.GroupByFields {
			value := getNestedFieldValue(docData, groupField)
			keyParts = append(keyParts, fmt.Sprintf("%v", value))
		}
		groupKey := strings.Join(keyParts, "|")

		if groups[groupKey] == nil {
			groups[groupKey] = []map[string]interface{}{}
		}
		groups[groupKey] = append(groups[groupKey], docData)
	}

	log.DefaultLogger.Info("GROUPING COMPLETE", "totalDocs", len(docs), "filteredDocs", len(filteredDocs), "totalGroups", len(groups))

	// Step 2: Calculate aggregations for each group
	type AggregatedResult struct {
		GroupValues     []interface{}
		AggregateValues []interface{}
		SortValue       float64 // Used for ORDER BY
	}

	var results []AggregatedResult

	for _, groupDocs := range groups {
		result := AggregatedResult{}

		// Extract group field values from the first document in the group
		if len(groupDocs) > 0 {
			for _, groupField := range queryInfo.GroupByFields {
				value := getNestedFieldValue(groupDocs[0], groupField)
				log.DefaultLogger.Info("Group field extraction", "field", groupField, "value", value, "docData", groupDocs[0])
				result.GroupValues = append(result.GroupValues, value)
			}
		}

		// Calculate aggregates
		for _, aggField := range queryInfo.AggregateFields {
			var aggregateValue interface{}

			switch aggField.Function {
			case "COUNT":
				aggregateValue = float64(len(groupDocs))
			case "SUM":
				sum := 0.0
				for _, doc := range groupDocs {
					if val := getNestedFieldValue(doc, aggField.Field); val != nil {
						if numVal, err := convertToFloat(val); err == nil {
							sum += numVal
						}
					}
				}
				aggregateValue = sum
			case "AVG":
				sum := 0.0
				count := 0
				for _, doc := range groupDocs {
					if val := getNestedFieldValue(doc, aggField.Field); val != nil {
						if numVal, err := convertToFloat(val); err == nil {
							sum += numVal
							count++
						}
					}
				}
				if count > 0 {
					aggregateValue = sum / float64(count)
				} else {
					aggregateValue = 0.0
				}
			case "MIN":
				var min *float64
				for _, doc := range groupDocs {
					if val := getNestedFieldValue(doc, aggField.Field); val != nil {
						if numVal, err := convertToFloat(val); err == nil {
							if min == nil || numVal < *min {
								min = &numVal
							}
						}
					}
				}
				if min != nil {
					aggregateValue = *min
				} else {
					aggregateValue = 0.0
				}
			case "MAX":
				var max *float64
				for _, doc := range groupDocs {
					if val := getNestedFieldValue(doc, aggField.Field); val != nil {
						if numVal, err := convertToFloat(val); err == nil {
							if max == nil || numVal > *max {
								max = &numVal
							}
						}
					}
				}
				if max != nil {
					aggregateValue = *max
				} else {
					aggregateValue = 0.0
				}
			default:
				aggregateValue = 0.0
			}

			result.AggregateValues = append(result.AggregateValues, aggregateValue)

			// Set sort value for ORDER BY (check multiple possible matches)
			if queryInfo.OrderField != "" {
				isMatch := false

				// Check direct alias match
				if queryInfo.OrderField == aggField.Alias {
					isMatch = true
				}

				// Check if ORDER BY matches the cleaned field name
				cleanedAlias := aggField.Alias
				if strings.Contains(cleanedAlias, "(") && strings.Contains(cleanedAlias, ")") {
					if strings.Contains(strings.ToUpper(cleanedAlias), " AS ") {
						parts := strings.Split(cleanedAlias, " ")
						for i, part := range parts {
							if strings.ToUpper(part) == "AS" && i+1 < len(parts) {
								cleanedAlias = parts[i+1]
								break
							}
						}
					} else {
						cleanedAlias = strings.ToLower(aggField.Function)
					}
				}

				if queryInfo.OrderField == cleanedAlias {
					isMatch = true
				}

				// Check function name match
				if queryInfo.OrderField == strings.ToLower(aggField.Function) {
					isMatch = true
				}

				if isMatch {
					if sortVal, err := convertToFloat(aggregateValue); err == nil {
						result.SortValue = sortVal
						log.DefaultLogger.Info("Set sort value during aggregation", "orderField", queryInfo.OrderField, "alias", aggField.Alias, "cleanedAlias", cleanedAlias, "value", sortVal)
					}
				}
			}
		}

		// If ORDER BY is on a group field, set sort value
		if queryInfo.OrderField != "" {
			for i, groupField := range queryInfo.GroupByFields {
				if queryInfo.OrderField == groupField && i < len(result.GroupValues) {
					if sortVal, err := convertToFloat(result.GroupValues[i]); err == nil {
						result.SortValue = sortVal
					}
				}
			}
		}

		results = append(results, result)
	}

	log.DefaultLogger.Info("Aggregated results", "totalResults", len(results))

	// Step 3: Apply ORDER BY if specified
	if queryInfo.OrderField != "" {
		log.DefaultLogger.Info("Applying ORDER BY", "field", queryInfo.OrderField, "direction", queryInfo.OrderDirection)

		// Validate that we have sort values set for all results
		validSortValues := true
		for i, result := range results {
			log.DefaultLogger.Debug("Result sort value", "index", i, "sortValue", result.SortValue, "groupValues", result.GroupValues, "aggregateValues", result.AggregateValues)
			if result.SortValue == 0 {
				// Try to match ORDER BY field with aggregate fields
				for j, aggField := range queryInfo.AggregateFields {
					if queryInfo.OrderField == aggField.Alias || queryInfo.OrderField == strings.ToLower(aggField.Function) {
						if j < len(result.AggregateValues) {
							if sortVal, err := convertToFloat(result.AggregateValues[j]); err == nil {
								results[i].SortValue = sortVal
								log.DefaultLogger.Info("Set sort value from aggregate", "index", i, "value", sortVal, "field", aggField.Alias)
							}
						}
					}
				}
			}
		}

		if validSortValues {
			// Sort results based on ORDER BY using bubble sort
			for i := 0; i < len(results)-1; i++ {
				for j := i + 1; j < len(results); j++ {
					shouldSwap := false

					if queryInfo.OrderDirection == "DESC" {
						shouldSwap = results[i].SortValue < results[j].SortValue
					} else {
						shouldSwap = results[i].SortValue > results[j].SortValue
					}

					if shouldSwap {
						results[i], results[j] = results[j], results[i]
					}
				}
			}
			log.DefaultLogger.Info("Sorting completed", "direction", queryInfo.OrderDirection)
		} else {
			log.DefaultLogger.Warn("Could not apply ORDER BY - invalid sort values")
		}
	}

	// Step 4: Apply LIMIT if specified
	if queryInfo.Limit > 0 && queryInfo.Limit < len(results) {
		log.DefaultLogger.Info("Applying LIMIT to GROUP BY results", "originalCount", len(results), "limitTo", queryInfo.Limit)
		results = results[:queryInfo.Limit]
	}

	// Step 5: Create data frame with grouped and aggregated data
	frame := data.NewFrame("response")

	// Add group fields
	for i, groupField := range queryInfo.GroupByFields {
		groupValues := make([]string, len(results))
		for j, result := range results {
			if i < len(result.GroupValues) {
				groupValues[j] = fmt.Sprintf("%v", result.GroupValues[i])
			}
		}
		frame.Fields = append(frame.Fields, data.NewField(groupField, nil, groupValues))
	}

	// Add aggregate fields with proper field names (use alias)
	for i, aggField := range queryInfo.AggregateFields {
		aggregateValues := make([]float64, len(results))
		for j, result := range results {
			if i < len(result.AggregateValues) {
				if val, err := convertToFloat(result.AggregateValues[i]); err == nil {
					aggregateValues[j] = val
				}
			}
		}

		// Use the alias from the query (e.g., "total" from "COUNT(*) as total")
		fieldName := aggField.Alias

		// Clean up the field name - remove function syntax if it's the default alias
		if strings.Contains(fieldName, "(") && strings.Contains(fieldName, ")") {
			// This looks like "COUNT(*) as total" or just "COUNT(*)" - extract the actual alias
			if strings.Contains(strings.ToUpper(fieldName), " AS ") {
				parts := strings.Split(fieldName, " ")
				// Find the part after "AS"
				for i, part := range parts {
					if strings.ToUpper(part) == "AS" && i+1 < len(parts) {
						fieldName = parts[i+1]
						break
					}
				}
			} else {
				// No alias, use function name
				fieldName = strings.ToLower(aggField.Function)
			}
		}

		log.DefaultLogger.Info("Creating aggregate field", "originalAlias", aggField.Alias, "finalFieldName", fieldName)

		frame.Fields = append(frame.Fields, data.NewField(fieldName, nil, aggregateValues))
	}

	response.Frames = append(response.Frames, frame)
	return response
}

// getNestedFieldValue extracts nested field values like "clientData.BrandCliente"
func getNestedFieldValue(doc map[string]interface{}, fieldPath string) interface{} {
	log.DefaultLogger.Info("Getting nested field value", "fieldPath", fieldPath, "docKeys", getDocumentKeys(doc))

	if !strings.Contains(fieldPath, ".") {
		value := doc[fieldPath]
		log.DefaultLogger.Info("Simple field lookup", "fieldPath", fieldPath, "value", value)
		return value
	}

	parts := strings.Split(fieldPath, ".")
	current := doc

	for i, part := range parts {
		if current == nil {
			return nil
		}

		if i == len(parts)-1 {
			// Last part - return the value
			return current[part]
		} else {
			// Navigate deeper
			if next, ok := current[part].(map[string]interface{}); ok {
				current = next
			} else {
				return nil
			}
		}
	}

	return nil
}

// getDocumentKeys helper function to debug document contents
func getDocumentKeys(doc map[string]interface{}) []string {
	keys := make([]string, 0, len(doc))
	for key := range doc {
		keys = append(keys, key)
	}
	return keys
}

// findGroupByIndex finds the index of "group by" clause accounting for potential whitespace and newlines
func findGroupByIndex(queryLower string) int {
	// Look for different variations of "group by" with potential whitespace
	patterns := []string{
		" group by ",
		"\ngroup by ",
		"\n  group by ",
		"\n\tgroup by ",
		"\r\ngroup by ",
		"\r\n  group by ",
	}

	for _, pattern := range patterns {
		if idx := strings.Index(queryLower, pattern); idx != -1 {
			return idx
		}
	}
	return -1
}

// findLimitIndex finds the index of "limit" clause accounting for potential whitespace and newlines
func findLimitIndex(queryLower string) int {
	// Look for different variations of "limit" with potential whitespace
	patterns := []string{
		" limit ",
		"\nlimit ",
		"\n  limit ",
		"\n\tlimit ",
		"\r\nlimit ",
		"\r\n  limit ",
	}

	for _, pattern := range patterns {
		if idx := strings.Index(queryLower, pattern); idx != -1 {
			return idx
		}
	}
	return -1
}

// convertToFloat converts various numeric types to float64
func convertToFloat(val interface{}) (float64, error) {
	switch v := val.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int32:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case string:
		return strconv.ParseFloat(v, 64)
	default:
		return 0, fmt.Errorf("cannot convert %T to float64", val)
	}
}

// applyManualFiltering applies WHERE clause filters manually to avoid Firestore index requirements
func (d *Datasource) applyManualFiltering(docs []*firestore.DocumentSnapshot, filters []FilterInfo) []*firestore.DocumentSnapshot {
	if len(filters) == 0 {
		return docs
	}

	if len(docs) == 0 {
		log.DefaultLogger.Info("MANUAL FILTERING: No documents to filter")
		return docs
	}

	log.DefaultLogger.Info("STARTING MANUAL FILTERING", "totalDocs", len(docs), "additionalFilters", len(filters))
	var filteredDocs []*firestore.DocumentSnapshot
	includedCount := 0
	excludedCount := 0

	for i, doc := range docs {
		if doc == nil {
			log.DefaultLogger.Warn("MANUAL FILTER: Skipping nil document", "index", i)
			excludedCount++
			continue
		}

		docData := doc.Data()
		if docData == nil {
			log.DefaultLogger.Warn("MANUAL FILTER: Skipping document with nil data", "index", i)
			excludedCount++
			continue
		}

		// Apply additional filters manually (since Firestore WHERE might not work with nested fields)
		passesFilters := true
		for _, filter := range filters {
			fieldValue := getNestedFieldValue(docData, filter.Field)
			if fieldValue == nil {
				log.DefaultLogger.Info("MANUAL FILTER: Field value is nil - EXCLUDING", "field", filter.Field, "expectedValue", filter.Value)
				passesFilters = false
				break
			}

			fieldValueStr := fmt.Sprintf("%v", fieldValue)
			expectedValueStr := fmt.Sprintf("%v", filter.Value)

			log.DefaultLogger.Info("MANUAL FILTER: Checking value", "field", filter.Field, "actualValue", fieldValueStr, "expectedValue", expectedValueStr, "operator", filter.Operator)

			if filter.Operator == "==" && fieldValueStr != expectedValueStr {
				log.DefaultLogger.Info("MANUAL FILTER: Value mismatch - EXCLUDING", "field", filter.Field, "actualValue", fieldValueStr, "expectedValue", expectedValueStr)
				passesFilters = false
				break
			} else if filter.Operator == "==" && fieldValueStr == expectedValueStr {
				log.DefaultLogger.Info("MANUAL FILTER: Value match - INCLUDING", "field", filter.Field, "value", fieldValueStr)
			}
		}

		if !passesFilters {
			excludedCount++
			continue // Skip this document
		}

		includedCount++
		filteredDocs = append(filteredDocs, doc)
	}

	log.DefaultLogger.Info("MANUAL FILTERING COMPLETE", "totalDocs", len(docs), "includedCount", includedCount, "excludedCount", excludedCount)
	return filteredDocs
}
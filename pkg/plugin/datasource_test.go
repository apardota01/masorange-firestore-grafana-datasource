package plugin

import (
	"cloud.google.com/go/firestore"
	"context"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

func TestQueryData(t *testing.T) {
	ds := Datasource{}

	var settings FirestoreSettings
	settings.ProjectId = "test"
	jsonSettings, err := json.Marshal(settings)
	if err != nil {
		t.Error(err)
	}

	var queries = make([]backend.DataQuery, len(queryTests))
	var byRefs = make(map[string]TestExpect, len(queryTests))
	for idx, queryTest := range queryTests {
		refID := fmt.Sprintf("ref%d", idx)
		queries[idx] = backend.DataQuery{
			RefID: refID,
			JSON:  []byte(fmt.Sprintf(`{"query": "%s"}`, queryTest.query)),
		}
		byRefs[refID] = queryTest
	}

	resp, err := ds.QueryData(
		context.Background(),
		&backend.QueryDataRequest{
			PluginContext: backend.PluginContext{
				DataSourceInstanceSettings: &backend.DataSourceInstanceSettings{
					JSONData: jsonSettings,
				},
			},
			Queries: queries,
		},
	)
	require.NoError(t, err)
	require.NotNil(t, resp.Responses)
	require.Len(t, resp.Responses, len(queryTests))

	for refId, response := range resp.Responses {
		require.NoError(t, response.Error)
		testExp := byRefs[refId]
		require.Len(t, response.Frames, 1)
		require.Len(t, response.Frames[0].Fields, testExp.columnsLength)
		for _, field := range response.Frames[0].Fields {
			require.Equal(t, testExp.rowsLength, field.Len())
		}
	}
}

type healthTest struct {
	settings  string
	decrypted map[string]string
	status    backend.HealthStatus
}

var healthTests = []healthTest{
	{``, nil, backend.HealthStatusError},
	{`{}`, nil, backend.HealthStatusError},
	{`{"ProjectId": "test"}`, map[string]string{"serviceAccount": "test"}, backend.HealthStatusError},
	{`{"ProjectId": "test"}`, map[string]string{"serviceAccount": `{}`}, backend.HealthStatusError},
	{`{"ProjectId": "test"}`, nil, backend.HealthStatusOk},
}

func TestCheckHealth(t *testing.T) {
	ds := Datasource{}
	for _, test := range healthTests {
		healthResponse, err := ds.CheckHealth(context.Background(), &backend.CheckHealthRequest{
			PluginContext: backend.PluginContext{
				DataSourceInstanceSettings: &backend.DataSourceInstanceSettings{
					JSONData:                []byte(test.settings),
					DecryptedSecureJSONData: test.decrypted,
				},
			},
		})
		require.NoError(t, err)
		require.NotNil(t, healthResponse)
		require.Equal(t, test.status, healthResponse.Status)
	}

}

type TestExpect struct {
	query         string
	rowsLength    int
	columnsLength int
	columns       []string
	frames        [][]interface{}
}

const FirestoreEmulatorHost = "FIRESTORE_EMULATOR_HOST"

var queryTests = []TestExpect{
	{
		query:         "select * from users",
		rowsLength:    5,
		columnsLength: 6,
	},
	//{
	//	query:   "select * from `users`",
	//	columns: []string{"id", "email", "username", "address", "name"},
	//	length:  "21",
	//},
	//{
	//	query:   "select id as uid, * from users",
	//	columns: []string{"uid", "id", "email", "username", "address", "name"},
	//	length:  "21",
	//},
	//{
	//	query:   "select *, username as uname from users",
	//	columns: []string{"id", "email", "username", "address", "name", "uname"},
	//	length:  "21",
	//},
	//{
	//	query:   "select  id as uid, *, username as uname from users",
	//	columns: []string{"uid", "id", "email", "username", "address", "name", "uname"},
	//	length:  "21",
	//},
	//{
	//	query:   "select id, email, address from users",
	//	columns: []string{"id", "email", "address"},
	//	length:  "21",
	//},
	//{
	//	query:   "select id, email, address from users limit 5",
	//	columns: []string{"id", "email", "address"},
	//	length:  "5",
	//},
	//{
	//	query:   "select id from users where email='aeatockj@psu.edu'",
	//	columns: []string{"id"},
	//	length:  "1",
	//	records: [][]interface{}{{float64(20)}},
	//},
	//{
	//	query:   "select id from users order by id desc limit 1",
	//	columns: []string{"id"},
	//	length:  "1",
	//	records: [][]interface{}{{float64(21)}},
	//},
	//{
	//	query:   "select LENGTH(username) as uLen from users where id = 8",
	//	columns: []string{"uLen"},
	//	length:  "1",
	//	records: [][]interface{}{{float64(6)}},
	//},
	//{
	//	query:   "select id from users where `address.city` = 'Glendale' and name = 'Eleanora'",
	//	columns: []string{"id"},
	//	length:  "1",
	//	records: [][]interface{}{{float64(10)}},
	//},
	//{
	//	query:   "select id > 0 as has_id from users where `address.city` = 'Glendale' and name = 'Eleanora'",
	//	columns: []string{"has_id"},
	//	length:  "1",
	//	records: [][]interface{}{{true}},
	//},
	//{
	//	query:   "select __name__ from users where id = 1",
	//	columns: []string{"__name__"},
	//	length:  "1",
	//	records: [][]interface{}{{"1"}},
	//},
	//{
	//	query:   "select id, email, username from users where id = 21",
	//	columns: []string{"id", "email", "username"},
	//	length:  "1",
	//	records: [][]interface{}{{float64(21), nil, "ckensleyk"}},
	//},
}

func newFirestoreTestClient(ctx context.Context) *firestore.Client {
	client, err := firestore.NewClient(ctx, "test")
	if err != nil {
		log.Fatalf("firebase.NewClient err: %v", err)
	}

	return client
}

func TestMain(m *testing.M) {
	// command to start firestore emulator
	cmd := exec.Command("gcloud", "beta", "emulators", "firestore", "start", "--host-port=localhost:8765")

	// this makes it killable
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// we need to capture it's output to know when it's started
	stderr, err := cmd.StderrPipe()
	if err != nil {
		log.Fatal(err)
	}
	defer stderr.Close()

	// start her up!
	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	}

	// ensure the process is killed when we're finished, even if an error occurs
	// (thanks to Brian Moran for suggestion)
	var result int
	defer func() {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		os.Exit(result)
	}()

	// we're going to wait until it's running to start
	var wg sync.WaitGroup
	wg.Add(1)

	// by starting a separate go routine
	go func() {
		// reading it's output
		buf := make([]byte, 256, 256)
		for {
			n, err := stderr.Read(buf[:])
			if err != nil {
				// until it ends
				if err == io.EOF {
					break
				}
				log.Fatalf("reading stderr %v", err)
			}

			if n > 0 {
				d := string(buf[:n])

				// only required if we want to see the emulator output
				log.Printf("%s", d)

				// checking for the message that it's started
				if strings.Contains(d, "Dev App Server is now running") {
					wg.Done()
				}

			}
		}
	}()

	// wait until the running message has been received
	wg.Wait()

	os.Setenv(FirestoreEmulatorHost, "localhost:8765")
	ctx := context.Background()
	users := newFirestoreTestClient(ctx).Collection("users")

	usersDataRaw, _ := os.ReadFile("../../test/data/users.json")
	var usersData []map[string]interface{}
	json.Unmarshal(usersDataRaw, &usersData)

	for _, user := range usersData {
		users.Doc(fmt.Sprintf("%v", user["id"].(float64))).Set(ctx, user)
	}

	//selectTests = append(selectTests, TestExpect{query: "select * from users", expected: usersData})
	// now it's running, we can run our unit tests
	result = m.Run()
}

//func TestSelectQueries(t *testing.T) {
//	for _, tt := range selectTests {
//		stmt := New(&util.Context{
//			ProjectId: "test",
//		}, tt.query)
//		actual, err := stmt.Execute()
//		if err != nil {
//			t.Error(err)
//		} else {
//			less := func(a, b string) bool { return a < b }
//			if cmp.Diff(tt.columns, actual.Columns, cmpopts.SortSlices(less)) != "" {
//				t.Errorf("QueryResult.Fields(%v): expected %v, actual %v", tt.query, tt.columns, actual.Columns)
//			}
//			if tt.length != "" && len(actual.Records) != first(strconv.Atoi(tt.length)) {
//				t.Errorf("len(QueryResult.Records)(%v): expected %v, actual %v", tt.query, len(actual.Records), tt.length)
//			}
//			if tt.records != nil && !cmp.Equal(actual.Records, tt.records) {
//				a, _ := json.Marshal(tt.records)
//				log.Println(string(a))
//				a, _ = json.Marshal(actual.Records)
//				log.Println(string(a))
//				t.Errorf("QueryResult.Records(%v): expected %v, actual %v", tt.query, tt.records, actual.Records)
//			}
//		}
//	}
//}
//
//func first(n int, _ error) int {
//	return n
//}

func TestAddTimeRangeFilter(t *testing.T) {
	from := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2023, 1, 31, 23, 59, 59, 0, time.UTC)
	timeRange := backend.TimeRange{From: from, To: to}

	tests := []struct {
		name     string
		query    string
		timeField string
		expected string
	}{
		{
			name:      "Simple select without WHERE clause",
			query:     "SELECT * FROM users",
			timeField: "createdAt",
			expected:  "SELECT * FROM users where createdAt >= '2023-01-01T00:00:00Z' and createdAt <= '2023-01-31T23:59:59Z'",
		},
		{
			name:      "Query with existing WHERE clause",
			query:     "SELECT * FROM users WHERE status = 'active'",
			timeField: "timestamp",
			expected:  "SELECT * FROM users WHERE status = 'active' and timestamp >= '2023-01-01T00:00:00Z' and timestamp <= '2023-01-31T23:59:59Z'",
		},
		{
			name:      "Query with ORDER BY",
			query:     "SELECT * FROM users ORDER BY name",
			timeField: "created_at",
			expected:  "SELECT * FROM users where created_at >= '2023-01-01T00:00:00Z' and created_at <= '2023-01-31T23:59:59Z' ORDER BY name",
		},
		{
			name:      "Query with LIMIT",
			query:     "SELECT * FROM users LIMIT 10",
			timeField: "updatedAt",
			expected:  "SELECT * FROM users where updatedAt >= '2023-01-01T00:00:00Z' and updatedAt <= '2023-01-31T23:59:59Z' LIMIT 10",
		},
		{
			name:      "Complex query with WHERE, ORDER BY and LIMIT",
			query:     "SELECT * FROM users WHERE active = true ORDER BY name LIMIT 5",
			timeField: "lastLogin",
			expected:  "SELECT * FROM users WHERE active = true and lastLogin >= '2023-01-01T00:00:00Z' and lastLogin <= '2023-01-31T23:59:59Z' ORDER BY name LIMIT 5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := addTimeRangeFilter(tt.query, tt.timeField, timeRange)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestReplaceGrafanaVariables(t *testing.T) {
	from := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2023, 1, 31, 23, 59, 59, 0, time.UTC)
	timeRange := backend.TimeRange{From: from, To: to}

	tests := []struct {
		name     string
		query    string
		expected string
	}{
		{
			name:     "Query with $__from variable",
			query:    "SELECT * FROM events WHERE timestamp >= $__from",
			expected: "SELECT * FROM events WHERE timestamp >= '2023-01-01T00:00:00Z'",
		},
		{
			name:     "Query with $__to variable",
			query:    "SELECT * FROM events WHERE timestamp <= $__to",
			expected: "SELECT * FROM events WHERE timestamp <= '2023-01-31T23:59:59Z'",
		},
		{
			name:     "Query with both variables",
			query:    "SELECT * FROM events WHERE timestamp >= $__from AND timestamp <= $__to",
			expected: "SELECT * FROM events WHERE timestamp >= '2023-01-01T00:00:00Z' AND timestamp <= '2023-01-31T23:59:59Z'",
		},
		{
			name:     "Complex query with variables",
			query:    "SELECT * FROM events WHERE type = 'error' AND createdAt BETWEEN $__from AND $__to ORDER BY createdAt",
			expected: "SELECT * FROM events WHERE type = 'error' AND createdAt BETWEEN '2023-01-01T00:00:00Z' AND '2023-01-31T23:59:59Z' ORDER BY createdAt",
		},
		{
			name:     "Query without variables",
			query:    "SELECT * FROM events WHERE type = 'error'",
			expected: "SELECT * FROM events WHERE type = 'error'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := replaceGrafanaVariables(tt.query, timeRange)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestContainsGrafanaVariables(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		expected bool
	}{
		{
			name:     "Query with $__from",
			query:    "SELECT * FROM events WHERE timestamp >= $__from",
			expected: true,
		},
		{
			name:     "Query with $__to",
			query:    "SELECT * FROM events WHERE timestamp <= $__to",
			expected: true,
		},
		{
			name:     "Query with both variables",
			query:    "SELECT * FROM events WHERE timestamp >= $__from AND timestamp <= $__to",
			expected: true,
		},
		{
			name:     "Query without variables",
			query:    "SELECT * FROM events WHERE type = 'error'",
			expected: false,
		},
		{
			name:     "Query with other variables",
			query:    "SELECT * FROM events WHERE type = '$variable'",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := containsGrafanaVariables(tt.query)
			require.Equal(t, tt.expected, result)
		})
	}
}

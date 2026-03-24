package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// airflowClient performs authenticated requests to the Airflow REST API.
// Token is fetched lazily and cached.
type airflowClient struct {
	baseURL  string
	username string
	password string
	token    string
	tokenExp time.Time
}

func newAirflowClient() (*airflowClient, error) {
	baseURL := os.Getenv("AIRFLOW_API_URL")
	if baseURL == "" {
		return nil, fmt.Errorf("AIRFLOW_API_URL not set")
	}
	return &airflowClient{
		baseURL:  baseURL,
		username: os.Getenv("AIRFLOW_API_USER"),
		password: os.Getenv("AIRFLOW_API_PASSWORD"),
	}, nil
}

// ensureToken fetches (or refreshes) the bearer token.
func (c *airflowClient) ensureToken(ctx context.Context) error {
	if c.token != "" && time.Now().Before(c.tokenExp) {
		return nil
	}
	payload, _ := json.Marshal(map[string]string{
		"username": c.username,
		"password": c.password,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/auth/token", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build auth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("auth request: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("airflow auth %d: %s", resp.StatusCode, data)
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return fmt.Errorf("parse auth response: %w", err)
	}

	token, _ := result["access_token"].(string)
	if token == "" {
		return fmt.Errorf("no access_token in auth response")
	}
	c.token = token
	c.tokenExp = time.Now().Add(30 * time.Minute)
	return nil
}

// get performs an authenticated GET request.
func (c *airflowClient) get(ctx context.Context, path string) (map[string]any, error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("airflow %d: %s", resp.StatusCode, data)
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return result, nil
}

// post performs an authenticated POST request with a JSON body.
func (c *airflowClient) post(ctx context.Context, path string, body map[string]any) (map[string]any, error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http post: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("airflow %d: %s", resp.StatusCode, data)
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return result, nil
}

// GetDagStatusTool returns the latest run status for a DAG.
type GetDagStatusTool struct{}

func (t *GetDagStatusTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "get_dag_status",
		Description: "Get the latest run status for an Airflow DAG.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"dag_id": map[string]any{
					"type":        "string",
					"description": "The DAG identifier",
				},
			},
			"required": []string{"dag_id"},
		},
	}
}

func (t *GetDagStatusTool) Execute(args map[string]any) ToolResult {
	return t.ExecuteWithContext(context.Background(), args)
}

func (t *GetDagStatusTool) ExecuteWithContext(ctx context.Context, args map[string]any) ToolResult {
	dagID, _ := args["dag_id"].(string)
	if dagID == "" {
		return ToolResult{IsError: true, Content: "dag_id is required"}
	}
	client, err := newAirflowClient()
	if err != nil {
		return ToolResult{IsError: true, Content: err.Error()}
	}
	result, err := client.get(ctx, fmt.Sprintf("/dags/%s/dagRuns?limit=1&order_by=-execution_date", dagID))
	if err != nil {
		return ToolResult{IsError: true, Content: fmt.Sprintf("get dag runs: %v", err)}
	}
	runs, _ := result["dag_runs"].([]any)
	if len(runs) == 0 {
		return ToolResult{Content: fmt.Sprintf("No runs found for DAG %s", dagID)}
	}
	run, _ := runs[0].(map[string]any)
	state, _ := run["state"].(string)
	execDate, _ := run["execution_date"].(string)
	return ToolResult{Content: fmt.Sprintf("DAG %s latest run: state=%s, execution_date=%s", dagID, state, execDate)}
}

// ListDagRunsTool lists recent DAG runs.
type ListDagRunsTool struct{}

func (t *ListDagRunsTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "list_dag_runs",
		Description: "List recent runs for an Airflow DAG.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"dag_id": map[string]any{
					"type":        "string",
					"description": "The DAG identifier",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of runs to return (default: 5)",
				},
			},
			"required": []string{"dag_id"},
		},
	}
}

func (t *ListDagRunsTool) Execute(args map[string]any) ToolResult {
	return t.ExecuteWithContext(context.Background(), args)
}

func (t *ListDagRunsTool) ExecuteWithContext(ctx context.Context, args map[string]any) ToolResult {
	dagID, _ := args["dag_id"].(string)
	if dagID == "" {
		return ToolResult{IsError: true, Content: "dag_id is required"}
	}
	limit := 5
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}
	client, err := newAirflowClient()
	if err != nil {
		return ToolResult{IsError: true, Content: err.Error()}
	}
	result, err := client.get(ctx, fmt.Sprintf("/dags/%s/dagRuns?limit=%d&order_by=-execution_date", dagID, limit))
	if err != nil {
		return ToolResult{IsError: true, Content: fmt.Sprintf("list dag runs: %v", err)}
	}
	runs, _ := result["dag_runs"].([]any)
	if len(runs) == 0 {
		return ToolResult{Content: fmt.Sprintf("No runs found for DAG %s", dagID)}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Recent runs for DAG %s:\n", dagID)
	for _, r := range runs {
		run, _ := r.(map[string]any)
		state, _ := run["state"].(string)
		execDate, _ := run["execution_date"].(string)
		runID, _ := run["dag_run_id"].(string)
		fmt.Fprintf(&sb, "  - %s [%s] %s\n", runID, state, execDate)
	}
	return ToolResult{Content: sb.String()}
}

// ListDagsTool lists all DAGs with their latest run status.
type ListDagsTool struct{}

func (t *ListDagsTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "list_dags",
		Description: "List all Airflow DAGs with their latest run status.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

func (t *ListDagsTool) Execute(args map[string]any) ToolResult {
	return t.ExecuteWithContext(context.Background(), args)
}

func (t *ListDagsTool) ExecuteWithContext(ctx context.Context, _ map[string]any) ToolResult {
	client, err := newAirflowClient()
	if err != nil {
		return ToolResult{IsError: true, Content: err.Error()}
	}
	result, err := client.get(ctx, "/dags?limit=100")
	if err != nil {
		return ToolResult{IsError: true, Content: fmt.Sprintf("list dags: %v", err)}
	}
	dags, _ := result["dags"].([]any)
	if len(dags) == 0 {
		return ToolResult{Content: "No DAGs found."}
	}
	var sb strings.Builder
	sb.WriteString("DAGs:\n")
	for _, d := range dags {
		dag, _ := d.(map[string]any)
		dagID, _ := dag["dag_id"].(string)
		isPaused, _ := dag["is_paused"].(bool)
		paused := ""
		if isPaused {
			paused = " [paused]"
		}
		fmt.Fprintf(&sb, "  - %s%s\n", dagID, paused)
	}
	return ToolResult{Content: sb.String()}
}

// TriggerDagTool triggers a new DAG run.
type TriggerDagTool struct{}

func (t *TriggerDagTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "trigger_dag",
		Description: "Trigger a new run for an Airflow DAG.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"dag_id": map[string]any{
					"type":        "string",
					"description": "The DAG identifier",
				},
				"conf": map[string]any{
					"type":        "object",
					"description": "Optional configuration to pass to the DAG run",
				},
			},
			"required": []string{"dag_id"},
		},
	}
}

func (t *TriggerDagTool) Execute(args map[string]any) ToolResult {
	return t.ExecuteWithContext(context.Background(), args)
}

func (t *TriggerDagTool) ExecuteWithContext(ctx context.Context, args map[string]any) ToolResult {
	dagID, _ := args["dag_id"].(string)
	if dagID == "" {
		return ToolResult{IsError: true, Content: "dag_id is required"}
	}
	client, err := newAirflowClient()
	if err != nil {
		return ToolResult{IsError: true, Content: err.Error()}
	}
	body := map[string]any{}
	if conf, ok := args["conf"]; ok {
		body["conf"] = conf
	}
	result, err := client.post(ctx, fmt.Sprintf("/dags/%s/dagRuns", dagID), body)
	if err != nil {
		return ToolResult{IsError: true, Content: fmt.Sprintf("trigger dag: %v", err)}
	}
	runID, _ := result["dag_run_id"].(string)
	state, _ := result["state"].(string)
	return ToolResult{Content: fmt.Sprintf("DAG %s triggered: run_id=%s, state=%s", dagID, runID, state)}
}

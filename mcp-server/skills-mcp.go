// Skills and Processes MCP Server
// Provides MCP tools to query local skills and processes data
// This allows the LLM to dynamically retrieve skills, competencies, and process information

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// SkillsDirectory represents the structure of the skills directory JSON
type SkillsDirectory struct {
	LastUpdated string                    `json:"last_updated"`
	Categories  map[string]SkillsCategory `json:"categories"`
}

// SkillsCategory represents a category of skills
type SkillsCategory struct {
	Name   string  `json:"name"`
	Skills []Skill `json:"skills"`
}

// Skill represents an individual skill
type Skill struct {
	SkillID          string `json:"skill_id"`
	Name             string `json:"name"`
	Level            string `json:"level"`
	Description      string `json:"description"`
	TrainingRequired string `json:"training_required"`
	Assessor         string `json:"assessor"`
	ValidityMonths   int    `json:"validity_months"`
}

// Processes represents the structure of the processes JSON
type Processes struct {
	LastUpdated string    `json:"last_updated"`
	Processes   []Process `json:"processes"`
}

// Process represents a process with its phases
type Process struct {
	ProcessID   string         `json:"process_id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Owner       string         `json:"owner"`
	OwnerTitle  string         `json:"owner_title"`
	OwnerEmail  string         `json:"owner_email"`
	Phases      []ProcessPhase `json:"phases"`
}

// ProcessPhase represents a phase of a process
type ProcessPhase struct {
	Phase    string   `json:"phase"`
	Duration int      `json:"duration_weeks"`
	Steps    []string `json:"steps"`
}

// SkillsProcessManager manages access to skills and process data
type SkillsProcessManager struct {
	skillsPath  string
	processPath string
	skillsData  *SkillsDirectory
	processData *Processes
}

// NewSkillsProcessManager creates a new manager
func NewSkillsProcessManager(skillsPath, processPath string) *SkillsProcessManager {
	return &SkillsProcessManager{
		skillsPath:  skillsPath,
		processPath: processPath,
	}
}

// LoadData loads the skills and processes data
func (m *SkillsProcessManager) LoadData() error {
	if m.skillsData == nil {
		data, err := os.ReadFile(m.skillsPath)
		if err != nil {
			return fmt.Errorf("failed to read skills data: %w", err)
		}
		if err := json.Unmarshal(data, &m.skillsData); err != nil {
			return fmt.Errorf("failed to parse skills data: %w", err)
		}
	}

	if m.processData == nil {
		data, err := os.ReadFile(m.processPath)
		if err != nil {
			return fmt.Errorf("failed to read processes data: %w", err)
		}
		if err := json.Unmarshal(data, &m.processData); err != nil {
			return fmt.Errorf("failed to parse processes data: %w", err)
		}
	}

	return nil
}

// ListSkills lists all skills in a category
func (m *SkillsProcessManager) ListSkills(category string) []Skill {
	if m.skillsData == nil {
		return nil
	}

	cat, ok := m.skillsData.Categories[category]
	if !ok {
		return nil
	}

	return cat.Skills
}

// SearchSkills searches for skills by keyword
func (m *SkillsProcessManager) SearchSkills(keyword string) []Skill {
	if m.skillsData == nil {
		return nil
	}

	var results []Skill
	keyword = strings.ToLower(keyword)

	for _, cat := range m.skillsData.Categories {
		for _, skill := range cat.Skills {
			if strings.Contains(strings.ToLower(skill.Name), keyword) ||
				strings.Contains(strings.ToLower(skill.Description), keyword) {
				results = append(results, skill)
			}
		}
	}

	return results
}

// GetProcess retrieves a specific process by ID
func (m *SkillsProcessManager) GetProcess(processID string) *Process {
	if m.processData == nil {
		return nil
	}

	for i := range m.processData.Processes {
		if m.processData.Processes[i].ProcessID == processID {
			return &m.processData.Processes[i]
		}
	}

	return nil
}

// SearchProcesses searches for processes by keyword
func (m *SkillsProcessManager) SearchProcesses(keyword string) []Process {
	if m.processData == nil {
		return nil
	}

	var results []Process
	keyword = strings.ToLower(keyword)

	for i := range m.processData.Processes {
		proc := &m.processData.Processes[i]
		if strings.Contains(strings.ToLower(proc.Name), keyword) ||
			strings.Contains(strings.ToLower(proc.Description), keyword) {
			results = append(results, *proc)
		}
	}

	return results
}

// GetProcessOwner returns the owner of a process
func (m *SkillsProcessManager) GetProcessOwner(processID string) (string, string, string) {
	proc := m.GetProcess(processID)
	if proc == nil {
		return "", "", ""
	}
	return proc.Owner, proc.OwnerTitle, proc.OwnerEmail
}

// MCP Tool Handlers

func handleSkillsList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	manager := getSkillsProcessManager()
	if err := manager.LoadData(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	category := req.GetString("category", "")

	if category == "" {
		// List all categories
		var sb strings.Builder
		sb.WriteString("Available skill categories:\n\n")

		for catID, cat := range manager.skillsData.Categories {
			sb.WriteString(fmt.Sprintf("### %s\n", cat.Name))
			sb.WriteString(fmt.Sprintf("- Category ID: %s\n", catID))
			sb.WriteString(fmt.Sprintf("- Skills: %d\n\n", len(cat.Skills)))
		}

		return mcp.NewToolResultText(sb.String()), nil
	}

	// List skills in a specific category
	skills := manager.ListSkills(category)
	if skills == nil {
		return mcp.NewToolResultError(fmt.Sprintf("Category not found: %s. Available categories: %v", category, getAvailableCategories())), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Skills in category: %s\n\n", manager.skillsData.Categories[category].Name))

	for _, skill := range skills {
		sb.WriteString(fmt.Sprintf("## %s (ID: %s)\n", skill.Name, skill.SkillID))
		sb.WriteString(fmt.Sprintf("- Level: %s\n", skill.Level))
		sb.WriteString(fmt.Sprintf("- Description: %s\n", skill.Description))
		sb.WriteString(fmt.Sprintf("- Training: %s\n", skill.TrainingRequired))
		sb.WriteString(fmt.Sprintf("- Assessor: %s\n", skill.Assessor))
		sb.WriteString(fmt.Sprintf("- Validity: %d months\n\n", skill.ValidityMonths))
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func handleSkillsSearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	manager := getSkillsProcessManager()
	if err := manager.LoadData(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	keyword := req.GetString("keyword", "")

	if keyword == "" {
		return mcp.NewToolResultError("Missing required parameter: keyword"), nil
	}

	skills := manager.SearchSkills(keyword)

	if skills == nil {
		return mcp.NewToolResultText(fmt.Sprintf("No skills found matching: %s\n\nTry searching for: 'collection', 'phlebotomy', 'quality', 'administration', etc.", keyword)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d skills matching '%s':\n\n", len(skills), keyword))

	for _, skill := range skills {
		sb.WriteString(fmt.Sprintf("## %s (ID: %s)\n", skill.Name, skill.SkillID))
		sb.WriteString(fmt.Sprintf("- Level: %s\n", skill.Level))
		sb.WriteString(fmt.Sprintf("- Description: %s\n", skill.Description))
		sb.WriteString(fmt.Sprintf("- Training: %s\n", skill.TrainingRequired))
		sb.WriteString(fmt.Sprintf("- Assessor: %s\n", skill.Assessor))
		sb.WriteString(fmt.Sprintf("- Validity: %d months\n\n", skill.ValidityMonths))
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func handleProcessGetSteps(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	manager := getSkillsProcessManager()
	if err := manager.LoadData(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	processID := req.GetString("process_id", "")

	if processID == "" {
		return mcp.NewToolResultError("Missing required parameter: process_id"), nil
	}

	proc := manager.GetProcess(processID)
	if proc == nil {
		return mcp.NewToolResultError(fmt.Sprintf("Process not found: %s. Available processes: %v", processID, getAvailableProcesses())), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Process: %s\n\n", proc.Name))
	sb.WriteString(fmt.Sprintf("**ID:** %s\n", proc.ProcessID))
	sb.WriteString(fmt.Sprintf("**Owner:** %s (%s) — %s\n", proc.Owner, proc.OwnerTitle, proc.OwnerEmail))
	sb.WriteString(fmt.Sprintf("**Description:** %s\n\n", proc.Description))
	sb.WriteString("## Process Steps\n\n")

	for i, phase := range proc.Phases {
		sb.WriteString(fmt.Sprintf("### Phase %d: %s (%d weeks)\n", i+1, phase.Phase, phase.Duration))
		sb.WriteString("- Steps:\n")
		for j, step := range phase.Steps {
			sb.WriteString(fmt.Sprintf("  %d. %s\n", j+1, step))
		}
		sb.WriteString("\n")
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func handleProcessGetOwner(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	manager := getSkillsProcessManager()
	if err := manager.LoadData(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	processID := req.GetString("process_id", "")

	if processID == "" {
		return mcp.NewToolResultError("Missing required parameter: process_id"), nil
	}

	owner, ownerTitle, ownerEmail := manager.GetProcessOwner(processID)

	if owner == "" {
		return mcp.NewToolResultError(fmt.Sprintf("Process not found: %s", processID)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Process Owner: %s\n\n", processID))
	sb.WriteString(fmt.Sprintf("**Process ID:** %s\n", processID))
	sb.WriteString(fmt.Sprintf("**Owner:** %s\n", owner))
	sb.WriteString(fmt.Sprintf("**Title:** %s\n", ownerTitle))
	sb.WriteString(fmt.Sprintf("**Email:** %s\n", ownerEmail))

	return mcp.NewToolResultText(sb.String()), nil
}

func handleProcessSearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	manager := getSkillsProcessManager()
	if err := manager.LoadData(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	keyword := req.GetString("keyword", "")

	if keyword == "" {
		// List all processes
		var sb strings.Builder
		sb.WriteString("Available processes:\n\n")

		for _, proc := range manager.processData.Processes {
			sb.WriteString(fmt.Sprintf("## %s (ID: %s)\n", proc.Name, proc.ProcessID))
			sb.WriteString(fmt.Sprintf("%s\n", proc.Description))
			sb.WriteString(fmt.Sprintf("Owner: %s (%s)\n\n", proc.Owner, proc.OwnerTitle))
		}

		return mcp.NewToolResultText(sb.String()), nil
	}

	// Search processes by keyword
	processes := manager.SearchProcesses(keyword)

	if processes == nil {
		return mcp.NewToolResultText(fmt.Sprintf("No processes found matching: %s\n\nTry searching for: 'onboarding', 'collection', 'quality', 'maintenance', 'competency', etc.", keyword)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d processes matching '%s':\n\n", len(processes), keyword))

	for _, proc := range processes {
		sb.WriteString(fmt.Sprintf("## %s (ID: %s)\n", proc.Name, proc.ProcessID))
		sb.WriteString(fmt.Sprintf("Owner: %s (%s) — %s\n", proc.Owner, proc.OwnerTitle, proc.OwnerEmail))
		sb.WriteString(fmt.Sprintf("Phases: %d\n\n", len(proc.Phases)))

		for _, phase := range proc.Phases {
			sb.WriteString(fmt.Sprintf("- **%s** (%d weeks): %d steps\n", phase.Phase, phase.Duration, len(phase.Steps)))
		}
		sb.WriteString("\n")
	}

	return mcp.NewToolResultText(sb.String()), nil
}

// Helper functions
func getAvailableCategories() []string {
	manager := getSkillsProcessManager()
	if manager.skillsData == nil {
		if err := manager.LoadData(); err != nil {
			return nil
		}
	}

	var categories []string
	for catID := range manager.skillsData.Categories {
		categories = append(categories, catID)
	}
	return categories
}

func getAvailableProcesses() []string {
	manager := getSkillsProcessManager()
	if manager.processData == nil {
		if err := manager.LoadData(); err != nil {
			return nil
		}
	}

	var processes []string
	for _, proc := range manager.processData.Processes {
		processes = append(processes, proc.ProcessID)
	}
	return processes
}

// Global manager instance
var globalManager *SkillsProcessManager

func getSkillsProcessManager() *SkillsProcessManager {
	if globalManager == nil {
		skillsPath := os.Getenv("SKILLS_DIR_PATH")
		processPath := os.Getenv("PROC_DIR_PATH")
		globalManager = NewSkillsProcessManager(skillsPath, processPath)
	}
	return globalManager
}

// RegisterSkillsTools registers skills and processes MCP tools
func RegisterSkillsTools(s *server.MCPServer) error {
	// Check if skills data exists
	skillsPath := os.Getenv("SKILLS_DIR_PATH")
	processPath := os.Getenv("PROC_DIR_PATH")

	if skillsPath == "" {
		return fmt.Errorf("SKILLS_DIR_PATH environment variable not set — skills/tools unavailable")
	}
	if processPath == "" {
		return fmt.Errorf("PROC_DIR_PATH environment variable not set — processes tools unavailable")
	}

	if !fileExists(skillsPath) {
		return fmt.Errorf("skills directory file not found: %s", skillsPath)
	}
	if !fileExists(processPath) {
		return fmt.Errorf("processes file not found: %s", processPath)
	}

	s.AddTool(mcp.NewTool("skills_list",
		mcp.WithDescription("List all skills in a skill category, or list all available categories if no category is specified. Useful for finding what skills are available for a role or department."),
		mcp.WithString("category", mcp.Description("The skill category to list. If not provided, lists all categories. Available categories: laboratory_operations, clinical, operations, administration")),
	), handleSkillsList)

	s.AddTool(mcp.NewTool("skills_search",
		mcp.WithDescription("Search for skills by keyword. Returns skills matching the keyword in their name or description. Useful for finding specific competency requirements."),
		mcp.WithString("keyword", mcp.Required(), mcp.Description("Keyword to search for in skills (e.g., 'collection', 'quality', 'administration'))")),
	), handleSkillsSearch)

	s.AddTool(mcp.NewTool("process_get_steps",
		mcp.WithDescription("Get the full steps of a process, organized by phase. Returns process details including owner, description, phases, and steps."),
		mcp.WithString("process_id", mcp.Required(), mcp.Description("The process ID to get steps for. Available processes: PROC-001 (New Site Onboarding), PROC-002 (Specimen Collection), PROC-003 (Equipment Maintenance), PROC-004 (Quality Incident), PROC-005 (Competency Assessment)")),
	), handleProcessGetSteps)

	s.AddTool(mcp.NewTool("process_get_owner",
		mcp.WithDescription("Get the owner of a process. Returns the owner's name, title, and email address."),
		mcp.WithString("process_id", mcp.Required(), mcp.Description("The process ID to get the owner for.")),
	), handleProcessGetOwner)

	s.AddTool(mcp.NewTool("process_search",
		mcp.WithDescription("Search for processes by keyword, or list all processes if no keyword is specified. Returns process names, IDs, and descriptions. Useful for finding which process applies to a given situation."),
		mcp.WithString("keyword", mcp.Description("Keyword to search for in processes (e.g., 'onboarding', 'quality', 'collection')). If not provided, lists all processes.")),
	), handleProcessSearch)

	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

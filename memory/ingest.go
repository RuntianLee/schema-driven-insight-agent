package memory

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

type IngestOptions struct {
	Adapter string
}

type ManualOptions struct {
	Adapter string
}

type IngestReport struct {
	Inserted int
	Skipped  int
}

type manualNotesFile struct {
	Notes []ManualNote `yaml:"notes"`
}

type ManualNote struct {
	ID            string   `yaml:"id"`
	TaskID        string   `yaml:"task_id"`
	Question      string   `yaml:"question"`
	Summary       string   `yaml:"summary"`
	AnswerOutline string   `yaml:"answer_outline"`
	Tools         []string `yaml:"tools"`
	Tags          []string `yaml:"tags"`
	Score         float64  `yaml:"score"`
}

func IngestTrajectoryDB(ctx context.Context, store Store, trajDB *sql.DB, opts IngestOptions) (IngestReport, error) {
	if opts.Adapter == "" {
		return IngestReport{}, fmt.Errorf("adapter is required")
	}
	rows, err := trajDB.QueryContext(ctx, `
		WITH best_eval AS (
			SELECT trajectory_id, task_id, pass, value
			FROM (
				SELECT
					trajectory_id,
					task_id,
					pass,
					value,
					row_number() OVER (
						PARTITION BY trajectory_id
						ORDER BY pass DESC, value DESC, created_at DESC, task_id ASC
					) AS rn
				FROM eval_results
				WHERE evaluator_name = 'data_correctness'
			)
			WHERE rn = 1
		),
		tool_path AS (
			SELECT trajectory_id, group_concat(tool_name, ',') AS tools
			FROM (
				SELECT trajectory_id, tool_name, step_index
				FROM trajectory_steps
				WHERE tool_name IS NOT NULL
				ORDER BY trajectory_id, step_index
			)
			GROUP BY trajectory_id
		)
		SELECT
			t.trajectory_id,
			coalesce(t.task_class, ''),
			coalesce(t.outcome, ''),
			t.input_question,
			coalesce(t.final_output, ''),
			coalesce(er.task_id, ''),
			coalesce(er.pass, 0),
			coalesce(er.value, 0),
			coalesce(tp.tools, '')
		FROM trajectories t
		LEFT JOIN best_eval er
			ON er.trajectory_id = t.trajectory_id
		LEFT JOIN tool_path tp
			ON tp.trajectory_id = t.trajectory_id
		ORDER BY t.created_at ASC`)
	if err != nil {
		return IngestReport{}, fmt.Errorf("query trajectories: %w", err)
	}
	defer rows.Close()

	var report IngestReport
	for rows.Next() {
		var trajectoryID, taskClass, outcome, question, finalOutput, taskID, toolCSV string
		var pass int
		var value float64
		if err := rows.Scan(&trajectoryID, &taskClass, &outcome, &question, &finalOutput, &taskID, &pass, &value, &toolCSV); err != nil {
			return report, fmt.Errorf("scan trajectory memory source: %w", err)
		}
		if outcome != "success" || taskID == "" || pass != 1 {
			report.Skipped++
			continue
		}

		score := value
		if score <= 0 {
			score = 1
		}
		tools := splitCSV(toolCSV)
		if _, err := store.Upsert(ctx, Item{
			SourceType:    "eval",
			SourceID:      trajectoryID + ":data_correctness",
			Adapter:       opts.Adapter,
			TaskID:        taskID,
			TaskClass:     taskClass,
			Question:      question,
			Summary:       summarizeText(finalOutput, question, 240),
			AnswerOutline: toolPathOutline(tools),
			Tools:         tools,
			Tags:          compactStrings("eval", taskClass),
			Score:         score,
		}); err != nil {
			return report, fmt.Errorf("upsert memory from trajectory %s: %w", trajectoryID, err)
		}
		report.Inserted++
	}
	if err := rows.Err(); err != nil {
		return report, fmt.Errorf("iterate trajectories: %w", err)
	}
	return report, nil
}

func IngestManualNotes(ctx context.Context, store Store, r io.Reader, opts ManualOptions) (IngestReport, error) {
	if opts.Adapter == "" {
		return IngestReport{}, fmt.Errorf("adapter is required")
	}
	var file manualNotesFile
	if err := yaml.NewDecoder(r).Decode(&file); err != nil {
		return IngestReport{}, fmt.Errorf("parse manual notes: %w", err)
	}

	var report IngestReport
	for _, note := range file.Notes {
		if note.ID == "" || note.Question == "" || note.Summary == "" {
			report.Skipped++
			continue
		}
		score := note.Score
		if score == 0 {
			score = 1
		}
		if _, err := store.Upsert(ctx, Item{
			SourceType:    "manual",
			SourceID:      note.ID,
			Adapter:       opts.Adapter,
			TaskID:        note.TaskID,
			Question:      note.Question,
			Summary:       note.Summary,
			AnswerOutline: note.AnswerOutline,
			Tools:         note.Tools,
			Tags:          note.Tags,
			Score:         score,
		}); err != nil {
			return report, fmt.Errorf("upsert manual note %s: %w", note.ID, err)
		}
		report.Inserted++
	}
	return report, nil
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func summarizeText(primary, fallback string, maxRunes int) string {
	text := strings.TrimSpace(primary)
	if text == "" {
		text = strings.TrimSpace(fallback)
	}
	if maxRunes <= 0 || utf8.RuneCountInString(text) <= maxRunes {
		return text
	}
	runes := []rune(text)
	return string(runes[:maxRunes])
}

func toolPathOutline(tools []string) string {
	if len(tools) == 0 {
		return ""
	}
	return "Observed successful tool path: " + strings.Join(tools, " -> ")
}

func compactStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

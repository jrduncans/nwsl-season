package app

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/jrduncans/nwsl-season/internal/backtest"
)

const evaluationBaselineID = "straight-line-pace-v1"

type modelEvaluationPage struct {
	seasonPage
	Status        string
	SelectedModel string
	ChartData     string
	Models        []evaluationModelView
}

type evaluationModelView struct {
	ID   string
	Name string
}

type evaluationChartData struct {
	BaselineID string                 `json:"baseline_id"`
	Models     []evaluationChartModel `json:"models"`
}

type evaluationChartModel struct {
	ID      string                           `json:"id"`
	Name    string                           `json:"name"`
	Windows map[string]evaluationChartWindow `json:"windows"`
}

type evaluationChartWindow struct {
	Stages []evaluationChartStage `json:"stages"`
}

type evaluationChartStage struct {
	Label       string  `json:"label"`
	Progress    float64 `json:"progress"`
	PointsMAE   float64 `json:"points_mae"`
	PositionMAE float64 `json:"position_mae"`
}

func evaluationView(report backtest.Report) (modelEvaluationPage, error) {
	data := evaluationChartData{BaselineID: evaluationBaselineID}
	view := modelEvaluationPage{Status: report.Status, SelectedModel: report.SelectedModel, Models: []evaluationModelView{}}
	for _, model := range report.Models {
		view.Models = append(view.Models, evaluationModelView{ID: model.ID, Name: model.Name})
		chartModel := evaluationChartModel{ID: model.ID, Name: model.Name, Windows: map[string]evaluationChartWindow{}}
		for _, window := range []string{backtest.DevelopmentWindow, backtest.HeldoutWindow} {
			result, ok := model.Windows[window]
			if !ok {
				continue
			}
			stages := make([]evaluationChartStage, 0, len(result.Stages))
			for label, metrics := range result.Stages {
				progress, ok := stageProgress(label)
				if !ok {
					continue
				}
				stages = append(stages, evaluationChartStage{
					Label: label, Progress: progress, PointsMAE: metrics.PointsMAE.Mean, PositionMAE: metrics.PositionMAE.Mean,
				})
			}
			sort.Slice(stages, func(i, j int) bool { return stages[i].Progress < stages[j].Progress })
			chartModel.Windows[window] = evaluationChartWindow{Stages: stages}
		}
		data.Models = append(data.Models, chartModel)
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return modelEvaluationPage{}, fmt.Errorf("encode model evaluation chart data: %w", err)
	}
	view.ChartData = string(encoded)
	return view, nil
}

func stageProgress(label string) (float64, bool) {
	var from, to int
	if _, err := fmt.Sscanf(label, "%d-%d%%", &from, &to); err != nil || from < 0 || to <= from || to > 100 {
		return 0, false
	}
	return float64(from+to) / 2, true
}

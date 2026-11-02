package cli

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"

	"github.com/thekrauss/kubemanager/internal/cli/i18n"
	"github.com/thekrauss/kubemanager/internal/cli/ui"
)

// Mise en forme commune des commandes : états traduits et colorés, sortie
// JSON pour les scripts (--json).

// flagJSON : sortie machine des commandes de lecture.
var flagJSON bool

// printer : sortie mise en forme de la commande.
func printer(cmd *cobra.Command) *ui.Printer { return ui.New(cmd.OutOrStdout()) }

// wantJSON : --json demandé ; v est alors écrit tel quel.
func wantJSON(cmd *cobra.Command, v any) (bool, error) {
	if !flagJSON {
		return false, nil
	}
	return true, writeJSON(cmd.OutOrStdout(), v)
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// États d'un conteneur : tonalité et libellé traduit.
var workloadStates = map[string]struct {
	tone ui.Tone
	key  string
}{
	"RUNNING":           {ui.ToneOK, "st.running"},
	"DEPLOYED":          {ui.ToneOK, "st.running"},
	"STARTED":           {ui.ToneOK, "st.running"},
	"STARTING":          {ui.ToneProgress, "st.starting"},
	"RESTARTING":        {ui.ToneProgress, "st.starting"},
	"UPDATING":          {ui.ToneProgress, "st.updating"},
	"DELETING":          {ui.ToneProgress, "st.deleting"},
	"STOPPED":           {ui.ToneNeutral, "st.stopped"},
	"FAILED":            {ui.ToneError, "st.failed"},
	"HELM_ERROR":        {ui.ToneError, "st.failed"},
	"CRASHLOOP":         {ui.ToneError, "st.crashloop"},
	"IMAGE_PULL_FAILED": {ui.ToneError, "st.image_pull"},
	"CONFIG_ERROR":      {ui.ToneError, "st.config"},
	"OOM_KILLED":        {ui.ToneError, "st.oom"},
	"EVICTED":           {ui.ToneError, "st.evicted"},
}

func workloadStatus(p *ui.Printer, status string) string {
	if st, ok := workloadStates[status]; ok {
		return p.Status(st.tone, i18n.T(st.key))
	}
	return p.Status(ui.ToneNeutral, status)
}

// siteStatus : état d'un site.
func siteStatus(p *ui.Printer, status string) string {
	switch status {
	case "LIVE":
		return p.Status(ui.ToneOK, i18n.T("st.live"))
	case "EMPTY":
		return p.Status(ui.ToneNeutral, i18n.T("st.empty"))
	case "DELETING":
		return p.Status(ui.ToneProgress, i18n.T("st.deleting"))
	}
	return p.Status(ui.ToneNeutral, status)
}

// phaseLabel : étape d'un déploiement de conteneur, lisible.
func phaseLabel(phase string) string {
	switch phase {
	case "HELM_PREPARING", "HELM_CHART_LOADING":
		return i18n.T("phase.preparing")
	case "HELM_INSTALLING":
		return i18n.T("phase.starting")
	case "DEPLOYED", "STARTED":
		return i18n.T("phase.ready")
	}
	return phase
}

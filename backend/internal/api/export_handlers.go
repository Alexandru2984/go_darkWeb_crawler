package api

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"onion-spider/internal/database"

	gofpdf "github.com/go-pdf/fpdf"
	"github.com/xuri/excelize/v2"
)

// handleExport streams the user's nodes (or all nodes for admins) in the
// requested format. Concurrency: at most 1 export per user, and at most 4
// global exports in flight — anything beyond returns 429.
func (d *deps) handleExport(w http.ResponseWriter, r *http.Request) {
	uid := GetUserID(r)
	userSemAny, _ := d.exportPerUser.LoadOrStore(uid, make(chan struct{}, 1))
	userSem := userSemAny.(chan struct{})
	select {
	case userSem <- struct{}{}:
		defer func() { <-userSem }()
	default:
		WriteJSONError(w, http.StatusTooManyRequests, "You already have an export in progress — wait for it to finish")
		return
	}
	select {
	case d.exportGlobalSem <- struct{}{}:
		defer func() { <-d.exportGlobalSem }()
	default:
		WriteJSONError(w, http.StatusTooManyRequests, "Too many simultaneous exports on the server — try again in a few moments")
		return
	}

	format := r.URL.Query().Get("format")
	switch format {
	case "csv", "ndjson", "xlsx", "pdf", "graphml":
	default:
		format = "json"
	}
	ip := ClientIP(r)
	log.Printf("[AUDIT] GET /api/export ip=%s uid=%d format=%s", SanitizeForLog(ip), uid, format)

	rc := http.NewResponseController(w)
	rc.SetWriteDeadline(time.Now().Add(10 * time.Minute))

	isAdmin := IsAdmin(r)
	switch format {
	case "json":
		d.exportJSON(w, r, uid, isAdmin)
	case "ndjson":
		d.exportNDJSON(w, r, uid, isAdmin)
	case "csv":
		d.exportCSV(w, r, uid, isAdmin)
	case "xlsx":
		d.exportXLSX(w, r, uid, isAdmin)
	case "pdf":
		d.exportPDF(w, r, uid, isAdmin)
	case "graphml":
		d.exportGraphML(w, r, uid, isAdmin)
	}
}

func (d *deps) exportJSON(w http.ResponseWriter, r *http.Request, uid int, isAdmin bool) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte("["))
	first := true
	err := d.cfg.DB.ExportNodes(r.Context(), uid, isAdmin, func(n database.Node) error {
		b, marshalErr := json.Marshal(n)
		if marshalErr != nil {
			return marshalErr
		}
		if !first {
			w.Write([]byte(","))
		}
		first = false
		_, err := w.Write(b)
		return err
	})
	w.Write([]byte("]"))
	if err != nil {
		log.Printf("[EXPORT] JSON error: %v", err)
	}
}

func (d *deps) exportNDJSON(w http.ResponseWriter, r *http.Request, uid int, isAdmin bool) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Disposition", "attachment; filename=onion_spider_export.ndjson")
	enc := json.NewEncoder(w)
	err := d.cfg.DB.ExportNodes(r.Context(), uid, isAdmin, func(n database.Node) error { return enc.Encode(n) })
	if err != nil {
		log.Printf("[EXPORT] NDJSON error: %v", err)
	}
}

func (d *deps) exportCSV(w http.ResponseWriter, r *http.Request, uid int, isAdmin bool) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment; filename=onion_spider_export.csv")
	cw := csv.NewWriter(w)
	cw.Write([]string{"id", "url", "title", "status_code", "server_header", "processing_status", "category", "last_crawled_at"})
	err := d.cfg.DB.ExportNodes(r.Context(), uid, isAdmin, func(n database.Node) error {
		return cw.Write([]string{
			strconv.Itoa(n.ID), SanitizeCSVField(n.URL), SanitizeCSVField(n.Title),
			strconv.Itoa(n.StatusCode), SanitizeCSVField(n.ServerHeader),
			n.ProcessingStatus, n.Category, n.LastCrawledAt,
		})
	})
	cw.Flush()
	if err != nil {
		log.Printf("[EXPORT] CSV error: %v", err)
	}
}

func (d *deps) exportXLSX(w http.ResponseWriter, r *http.Request, uid int, isAdmin bool) {
	const xlsxRowCap = 10_000
	xf := excelize.NewFile()
	defer xf.Close()
	sheet := "Nodes"
	xf.SetSheetName("Sheet1", sheet)
	for col, h := range []string{"id", "url", "title", "status_code", "server_header", "processing_status", "category", "last_crawled_at"} {
		cell, _ := excelize.CoordinatesToCellName(col+1, 1)
		xf.SetCellValue(sheet, cell, h)
	}
	xlsxRow := 2
	err := d.cfg.DB.ExportNodes(r.Context(), uid, isAdmin, func(n database.Node) error {
		if xlsxRow-1 > xlsxRowCap {
			return nil
		}
		for col, v := range []interface{}{n.ID, SanitizeCSVField(n.URL), SanitizeCSVField(n.Title), n.StatusCode, SanitizeCSVField(n.ServerHeader), n.ProcessingStatus, n.Category, n.LastCrawledAt} {
			cell, _ := excelize.CoordinatesToCellName(col+1, xlsxRow)
			xf.SetCellValue(sheet, cell, v)
		}
		xlsxRow++
		return nil
	})
	if err != nil {
		log.Printf("[EXPORT] XLSX error: %v", err)
	}
	var xlsxBuf bytes.Buffer
	if err := xf.Write(&xlsxBuf); err != nil {
		WriteJSONError(w, http.StatusInternalServerError, "Error generating XLSX")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", "attachment; filename=onion_spider_export.xlsx")
	w.Write(xlsxBuf.Bytes())
}

func (d *deps) exportPDF(w http.ResponseWriter, r *http.Request, uid int, isAdmin bool) {
	const pdfRowCap = 5_000
	pf := gofpdf.New("L", "mm", "A4", "")
	pf.AddPage()
	pf.SetTitle("Onion Spider Export", false)
	pf.SetFont("Helvetica", "B", 8)
	type pdfCol struct {
		name  string
		width float64
	}
	cols := []pdfCol{{"ID", 12}, {"URL", 110}, {"Title", 50}, {"Status", 14}, {"Category", 28}, {"Last Crawled", 35}}
	pf.SetFillColor(50, 50, 50)
	pf.SetTextColor(255, 255, 255)
	for _, c := range cols {
		pf.CellFormat(c.width, 7, c.name, "1", 0, "C", true, 0, "")
	}
	pf.Ln(-1)
	pf.SetFont("Helvetica", "", 7)
	pf.SetTextColor(0, 0, 0)
	pdfRows := 0
	fillRow := false
	trunc := func(s string, max int) string {
		runes := []rune(s)
		if len(runes) > max {
			return string(runes[:max-1]) + "..."
		}
		return s
	}
	err := d.cfg.DB.ExportNodes(r.Context(), uid, isAdmin, func(n database.Node) error {
		if pdfRows >= pdfRowCap {
			return nil
		}
		if fillRow {
			pf.SetFillColor(240, 240, 240)
		} else {
			pf.SetFillColor(255, 255, 255)
		}
		pf.CellFormat(cols[0].width, 6, strconv.Itoa(n.ID), "1", 0, "R", true, 0, "")
		pf.CellFormat(cols[1].width, 6, trunc(n.URL, 100), "1", 0, "L", true, 0, "")
		pf.CellFormat(cols[2].width, 6, trunc(n.Title, 40), "1", 0, "L", true, 0, "")
		pf.CellFormat(cols[3].width, 6, strconv.Itoa(n.StatusCode), "1", 0, "C", true, 0, "")
		pf.CellFormat(cols[4].width, 6, n.Category, "1", 0, "L", true, 0, "")
		pf.CellFormat(cols[5].width, 6, n.LastCrawledAt, "1", 1, "L", true, 0, "")
		pdfRows++
		fillRow = !fillRow
		return nil
	})
	if err != nil {
		log.Printf("[EXPORT] PDF error: %v", err)
	}
	var pdfBuf bytes.Buffer
	if err := pf.Output(&pdfBuf); err != nil {
		WriteJSONError(w, http.StatusInternalServerError, "Error generating PDF")
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", "attachment; filename=onion_spider_export.pdf")
	w.Write(pdfBuf.Bytes())
}

func (d *deps) exportGraphML(w http.ResponseWriter, r *http.Request, uid int, isAdmin bool) {
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("Content-Disposition", "attachment; filename=onion_spider_export.graphml")
	fmt.Fprint(w, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	fmt.Fprint(w, "<graphml xmlns=\"http://graphml.graphdrawing.org/graphml\">\n")
	fmt.Fprint(w, "  <key id=\"d0\" for=\"node\" attr.name=\"url\" attr.type=\"string\"/>\n")
	fmt.Fprint(w, "  <key id=\"d1\" for=\"node\" attr.name=\"title\" attr.type=\"string\"/>\n")
	fmt.Fprint(w, "  <key id=\"d2\" for=\"node\" attr.name=\"category\" attr.type=\"string\"/>\n")
	fmt.Fprint(w, "  <graph id=\"G\" edgedefault=\"directed\">\n")
	xmlEsc := func(s string) string {
		var sb strings.Builder
		xml.EscapeText(&sb, []byte(s))
		return sb.String()
	}
	err := d.cfg.DB.ExportNodes(r.Context(), uid, isAdmin, func(n database.Node) error {
		fmt.Fprintf(w, "    <node id=\"n%d\">\n", n.ID)
		fmt.Fprintf(w, "      <data key=\"d0\">%s</data>\n", xmlEsc(n.URL))
		fmt.Fprintf(w, "      <data key=\"d1\">%s</data>\n", xmlEsc(n.Title))
		fmt.Fprintf(w, "      <data key=\"d2\">%s</data>\n", xmlEsc(n.Category))
		fmt.Fprint(w, "    </node>\n")
		return nil
	})
	if err != nil {
		log.Printf("[EXPORT] GraphML nodes error: %v", err)
	}
	err = d.cfg.DB.ExportGraphMLEdges(r.Context(), uid, isAdmin, func(ge database.GraphMLEdge) error {
		fmt.Fprintf(w, "    <edge source=\"n%d\" target=\"n%d\"/>\n", ge.SourceID, ge.TargetID)
		return nil
	})
	if err != nil {
		log.Printf("[EXPORT] GraphML edges error: %v", err)
	}
	fmt.Fprint(w, "  </graph>\n</graphml>\n")
}

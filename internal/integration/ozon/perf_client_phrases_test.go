package ozon

import (
	"archive/zip"
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var phrasesFallback = time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)

func TestParsePhrasesCSV_RussianHeaders(t *testing.T) {
	csvBody := "Дата;SKU;Поисковый запрос;Показы;Клики;Расход;Заказы;Средняя позиция\n" +
		"2026-08-01;123456;чехол iphone 15;1 200;45;123,50;3;4,2\n" +
		"2026-08-02;123456;Чехол Iphone 15;900;30;80,00;1;5,0\n" +
		";;Всего;2100;75;203,50;4;\n"

	rows, err := parsePhrasesCSV([]byte(csvBody), phrasesFallback)
	require.NoError(t, err)
	require.Len(t, rows, 2, "summary row must be skipped")

	first := rows[0]
	assert.Equal(t, int64(123456), first.SKU)
	assert.Equal(t, "чехол iphone 15", first.Query)
	assert.Equal(t, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), first.Date)
	assert.Equal(t, int64(1200), first.Views, "space thousand separator")
	assert.Equal(t, int64(45), first.Clicks)
	assert.InDelta(t, 123.50, first.SpendRub, 0.001, "comma decimal")
	assert.Equal(t, int64(3), first.Orders)
	require.NotNil(t, first.AvgPosition)
	assert.InDelta(t, 4.2, *first.AvgPosition, 0.001)

	// Queries are normalized to lowercase.
	assert.Equal(t, "чехол iphone 15", rows[1].Query)
}

func TestParsePhrasesCSV_PreambleAndCommaSeparator(t *testing.T) {
	csvBody := "Отчёт по фразам за период 2026-08-01 — 2026-08-07\n" +
		"date,sku,phrase,views,clicks,moneyspent,orders\n" +
		"2026-08-03,777,кроссовки мужские,500,20,42.10,2\n"

	rows, err := parsePhrasesCSV([]byte(csvBody), phrasesFallback)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, int64(777), rows[0].SKU)
	assert.Equal(t, "кроссовки мужские", rows[0].Query)
	assert.InDelta(t, 42.10, rows[0].SpendRub, 0.001)
	assert.Nil(t, rows[0].AvgPosition)
}

func TestParsePhrasesCSV_MissingDateUsesFallback(t *testing.T) {
	csvBody := "Поисковый запрос;Показы;Клики\nноутбук;100;5\n"
	rows, err := parsePhrasesCSV([]byte(csvBody), phrasesFallback)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, phrasesFallback, rows[0].Date)
}

func TestParsePhrasesCSV_NoQueryColumn(t *testing.T) {
	csvBody := "id;views;clicks\n1;2;3\n"
	_, err := parsePhrasesCSV([]byte(csvBody), phrasesFallback)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no recognizable query column")
}

func TestParsePhrasesJSON_RowsEnvelope(t *testing.T) {
	body := []byte(`{"rows":[
		{"date":"2026-08-01","sku":"123","phrase":"платье летнее","views":"100","clicks":7,"moneySpent":"15.5","orders":1,"avgPosition":"3.4"},
		{"date":"2026-08-01","searchQuery":"платье вечернее","views":50,"clicks":2,"spend":8,"orders":0}
	]}`)

	rows, err := parsePhrasesJSON(body, phrasesFallback)
	require.NoError(t, err)
	require.Len(t, rows, 2)

	// Numbers-as-strings are absorbed by the flex types (prod lesson).
	assert.Equal(t, int64(123), rows[0].SKU)
	assert.Equal(t, int64(100), rows[0].Views)
	assert.InDelta(t, 15.5, rows[0].SpendRub, 0.001)
	require.NotNil(t, rows[0].AvgPosition)
	assert.InDelta(t, 3.4, *rows[0].AvgPosition, 0.001)

	// searchQuery alias + "spend" alias work; missing position stays nil.
	assert.Equal(t, "платье вечернее", rows[1].Query)
	assert.InDelta(t, 8, rows[1].SpendRub, 0.001)
	assert.Nil(t, rows[1].AvgPosition)
}

func TestParsePhrasesJSON_ReportEnvelopeAndBareArray(t *testing.T) {
	report := []byte(`{"report":{"rows":[{"date":"2026-08-02","query":"рюкзак","views":10,"clicks":1,"moneySpent":2,"orders":0}]}}`)
	rows, err := parsePhrasesJSON(report, phrasesFallback)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "рюкзак", rows[0].Query)

	array := []byte(`[{"date":"2026-08-02","phrase":"рюкзак городской","views":10,"clicks":1,"moneySpent":2,"orders":0}]`)
	rows, err = parsePhrasesJSON(array, phrasesFallback)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "рюкзак городской", rows[0].Query)
}

func TestParsePhrasesJSON_PerCampaignMap(t *testing.T) {
	body := []byte(`{
		"111": {"report": {"rows": [{"phrase":"кружка","views":5,"clicks":1,"moneySpent":1,"orders":0,"date":"2026-08-03"}]}},
		"222": {"rows": [{"phrase":"тарелка","views":6,"clicks":2,"moneySpent":2,"orders":1,"date":"2026-08-03"}]}
	}`)
	rows, err := parsePhrasesJSON(body, phrasesFallback)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	queries := []string{rows[0].Query, rows[1].Query}
	assert.ElementsMatch(t, []string{"кружка", "тарелка"}, queries)
}

func TestParsePhrasesReport_DispatchAndZip(t *testing.T) {
	client := &PerfClient{}

	// ZIP with one CSV entry (the multi-campaign report shape).
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	entry, err := zw.Create("campaign_111.csv")
	require.NoError(t, err)
	_, err = entry.Write([]byte("Поисковый запрос;Показы;Клики;Расход;Заказы\nтермос;300;12;25,00;1\n"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	rows, err := client.parsePhrasesReport(buf.Bytes(), phrasesFallback)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "термос", rows[0].Query)
	assert.Equal(t, int64(300), rows[0].Views)

	// JSON dispatch.
	rows, err = client.parsePhrasesReport([]byte(`{"rows":[{"phrase":"термокружка","views":1,"clicks":0,"moneySpent":0,"orders":0}]}`), phrasesFallback)
	require.NoError(t, err)
	require.Len(t, rows, 1)

	// CSV dispatch.
	rows, err = client.parsePhrasesReport([]byte("phrase;views\nчайник;9\n"), phrasesFallback)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, int64(9), rows[0].Views)

	// Empty body → no rows, no error.
	rows, err = client.parsePhrasesReport([]byte("  \n"), phrasesFallback)
	require.NoError(t, err)
	assert.Empty(t, rows)
}

func TestWirePhraseRowToRow_QueryRequired(t *testing.T) {
	_, ok := wirePhraseRow{Views: 10}.toRow(phrasesFallback)
	assert.False(t, ok, "rows without any query text are dropped")
}

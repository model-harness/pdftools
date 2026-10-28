# Builds word-report.pdf with Microsoft Word, as Word itself exports a tagged PDF: a short report
# with a paragraph that wraps, as a report's does; a table ruled only under its header, whose
# narrow columns make Word break words inside them; and a code sample set as one shaded paragraph
# in Consolas whose lines are broken by hand (Shift+Enter), as Word users write one. The content is
# ours. Run from this directory on Windows with Word installed:
#
#   pwsh -File word-report.ps1
#   pwsh -File word-report.ps1 -Untagged    # word-report-untagged.pdf: the same, with no tags
#
# The PDF depends on Word's version; the one committed is what Word 16.0 wrote.
param([string]$Out = $PSScriptRoot, [switch]$Untagged)
$ErrorActionPreference = 'Stop'

$word = New-Object -ComObject Word.Application
$word.Visible = $false
try {
    $d = $word.Documents.Add()

    function Add-Paragraph([string]$text, [string]$style) {
        $r = $d.Paragraphs.Last.Range
        $r.Text = $text
        $r.Style = $d.Styles.Item($style)
        $r.InsertParagraphAfter()
    }

    Add-Paragraph 'Sailing Records' 'Heading 1'
    Add-Paragraph ('The office keeps a record of every sailing the ferry makes, from the first crossing in ' +
        'the morning to the last one at night. Each record is written by the crew when the ferry ' +
        'leaves the pier, checked by the purser when it arrives, and filed with the harbour master ' +
        'at the end of the week. A record that is missing a field is sent back to the crew, who ' +
        'complete it before the next sailing, and the office reads the records once a day to see ' +
        'how many passengers each route has carried.') 'Normal'
    Add-Paragraph 'Each sailing is recorded with the fields below.' 'Normal'

    # A table ruled only under its header row, Word's bottom border on each header cell, with a
    # first and second column too narrow for their longest words.
    $t = $d.Tables.Add($d.Paragraphs.Last.Range, 3, 3)
    $t.Borders.Enable = $false
    $rows = @(
        @('Field', 'Required', 'Description'),
        @('Confidence', 'Recommended', 'How sure the crew is of the count'),
        @('Timestamp', 'Yes', 'When the sailing left the pier')
    )
    for ($i = 0; $i -lt 3; $i++) {
        for ($j = 0; $j -lt 3; $j++) { $t.Cell($i + 1, $j + 1).Range.Text = $rows[$i][$j] }
    }
    $t.Rows.Item(1).Borders.Item(-3).LineStyle = 1   # wdBorderBottom
    $t.Columns.Item(1).Width = 50
    $t.Columns.Item(2).Width = 90
    $t.Columns.Item(3).Width = 200

    $d.Content.InsertParagraphAfter()
    Add-Paragraph 'Example' 'Heading 2'
    Add-Paragraph 'A record is written as a comment and a function:' 'Normal'

    # One paragraph, its lines broken by hand: `v is Word's manual line break.
    $r = $d.Paragraphs.Last.Range
    $r.Text = "# sailing record`v# Port: Oban`v`vdef record(sailing):`v    return sailing.id"
    $r.Style = $d.Styles.Item('Normal')
    $r.Font.Name = 'Consolas'
    $r.ParagraphFormat.Shading.BackgroundPatternColor = 15132390
    $r.InsertParagraphAfter()

    $r = $d.Paragraphs.Last.Range
    $r.Text = 'The records are kept for a year.'
    $r.Style = $d.Styles.Item('Normal')
    $r.Font.Name = 'Calibri'
    $r.ParagraphFormat.Shading.BackgroundPatternColor = -16777216   # wdColorAutomatic

    # wdExportFormatPDF, with document structure tags for accessibility.
    # Word signs a document with the name of whoever runs it; the fixture is signed by the repo.
    $author = [System.__ComObject].InvokeMember('Item', 'GetProperty', $null, $d.BuiltInDocumentProperties, @('Author'))
    [System.__ComObject].InvokeMember('Value', 'SetProperty', $null, $author, @('pdftools')) | Out-Null
    # Untagged, the export writes no structure tree: the layout path reads the same page.
    $name = if ($Untagged) { 'word-report-untagged.pdf' } else { 'word-report.pdf' }
    $d.ExportAsFixedFormat((Join-Path $Out $name), 17, $false, 0, 0, 1, 1, 0, $true, $true, 1, -not $Untagged, $true, $false)
    $d.Close($false)
}
finally {
    $word.Quit()
}

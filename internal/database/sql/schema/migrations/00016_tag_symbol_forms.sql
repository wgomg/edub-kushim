-- +goose Up
CREATE TEMP TABLE tag_symbol_pairs (spelled TEXT, symbol TEXT);
INSERT INTO tag_symbol_pairs VALUES
    ('c plus plus', 'c++'),
    ('c sharp', 'c#'),
    ('dot net', '.net'),
    ('asp net', 'asp.net'),
    ('vbnet', 'vb.net');

DELETE FROM document_tag dt
USING tag_symbol_pairs p, tag w, tag s
WHERE dt.tag_id = w.id
  AND w.name = p.spelled
  AND s.name = p.symbol
  AND EXISTS (
      SELECT 1 FROM document_tag dt2
      WHERE dt2.document_id = dt.document_id
        AND dt2.tag_id = s.id
  );

UPDATE document_tag dt
SET tag_id = s.id
FROM tag_symbol_pairs p, tag w, tag s
WHERE dt.tag_id = w.id
  AND w.name = p.spelled
  AND s.name = p.symbol;

DELETE FROM tag w
USING tag_symbol_pairs p
WHERE w.name = p.spelled;

UPDATE tag t
SET name = p.symbol
FROM tag_symbol_pairs p
WHERE t.name = p.spelled
  AND NOT EXISTS (SELECT 1 FROM tag x WHERE x.name = p.symbol);

DROP TABLE tag_symbol_pairs;
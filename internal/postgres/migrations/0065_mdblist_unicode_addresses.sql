-- MDBList list addresses may contain any letters, also æ, ø and å
-- ("julefilm-på-dansk"). The address parts are still single path segments:
-- no slash, backslash, query, fragment, percent sign, space or control
-- character, and not "." or "..". The application checks the same rule.
ALTER TABLE import_lists DROP CONSTRAINT import_lists_mdblist_source;
ALTER TABLE import_lists ADD CONSTRAINT import_lists_mdblist_source CHECK (
  provider <> 'mdblist' OR (
    char_length(source_owner) BETWEEN 1 AND 100 AND char_length(source_slug) BETWEEN 1 AND 200
    AND source_owner !~ '[/\\?#%[:space:][:cntrl:]]' AND source_slug !~ '[/\\?#%[:space:][:cntrl:]]'
    AND source_owner NOT IN ('.','..') AND source_slug NOT IN ('.','..')
  )
);

INSERT INTO document_type (name, description)
VALUES
    ('undetermined', 'document type has not been determined yet'),
    ('academic-paper', 'scholarly or scientific research paper'),
    ('article', 'news, column, blog, magazine'),
    ('book', 'full-length book or monograph'),
    ('tutorial', 'step-by-step tutorial or lesson'),
    ('guide', 'how-to guide or walkthrough'),
    ('manual', 'technical reference manual'),
    ('report', 'institutional or technical report'),
    ('other', 'any other text not fitting into another type')
ON CONFLICT (name) DO NOTHING;

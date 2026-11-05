-- +goose Up
-- the startup seeder owns the shipped alternate spellings: without a durable marker, removing one in
-- the vocabulary tab would silently come back on the next boot
ALTER TABLE tag_alias ADD COLUMN seeded BOOLEAN NOT NULL DEFAULT false;

-- the version marker cannot identify the shipped rows: the identity machinery also stamped 'v0' before
-- rule set v1, so the backfill matches the seeder's own alias list as it stood at this migration; rows
-- the seeder adds later carry seeded = true at insert time
UPDATE tag_alias SET seeded = true WHERE alias_name IN (
    'us', 'usa', 'u.s.', 'u.s.a.', 'united states of america',
    'uk', 'u.k.', 'britain', 'great britain',
    'uae', 'u.a.e.',
    'congo brazzaville', 'republic of the congo', 'congo kinshasa',
    'democratic republic of congo', 'democratic republic of the congo',
    'russian federation', 'dprk', 'republic of korea',
    'state of palestine', 'palestinian territories', 'czech republic',
    'turkiye', 'burma', 'swaziland', 'cape verde', 'east timor', 'macedonia',
    'holland', 'vatican', 'federated states of micronesia', 'cote d ivoire',
    'antigua barbuda', 'bosnia herzegovina', 'trinidad tobago',
    'st lucia', 'st. lucia', 'sahrawi arab democratic republic', 'christianism'
);

-- +goose Down
ALTER TABLE tag_alias DROP COLUMN IF EXISTS seeded;

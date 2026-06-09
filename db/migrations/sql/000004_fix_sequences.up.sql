-- V4__fix_sequences.sql
-- Fix sequence desynchronization caused by seed data inserting explicit ids.
-- Without this, the first application INSERT (posting a review) collides on the
-- primary key because reviews_id_seq still points at 1 while the seeded rows
-- already occupy higher ids.

-- Set the sequence for reviews table to the max id
SELECT setval('reviews_id_seq', (SELECT MAX(id) FROM reviews));

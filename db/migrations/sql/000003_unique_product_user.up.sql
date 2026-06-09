-- V3__unique_product_user.sql
-- Enforce one review per (product_id, user_id) at the database level.

ALTER TABLE reviews ADD CONSTRAINT uq_reviews_product_user UNIQUE (product_id, user_id);

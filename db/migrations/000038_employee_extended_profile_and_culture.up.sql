ALTER TABLE employee_personal_profiles
  ADD COLUMN IF NOT EXISTS bio text,
  ADD COLUMN IF NOT EXISTS avatar_url text,
  ADD COLUMN IF NOT EXISTS shirt_size text,
  ADD COLUMN IF NOT EXISTS favorite_food text,
  ADD COLUMN IF NOT EXISTS favorite_movies text,
  ADD COLUMN IF NOT EXISTS favorite_music text,
  ADD COLUMN IF NOT EXISTS coffee_tea text,
  ADD COLUMN IF NOT EXISTS fun_fact text;

ALTER TABLE users
  ADD COLUMN IF NOT EXISTS avatar_url text;

ALTER TABLE employee_personal_profiles
  DROP COLUMN IF EXISTS bio,
  DROP COLUMN IF EXISTS avatar_url,
  DROP COLUMN IF EXISTS shirt_size,
  DROP COLUMN IF EXISTS favorite_food,
  DROP COLUMN IF EXISTS favorite_movies,
  DROP COLUMN IF EXISTS favorite_music,
  DROP COLUMN IF EXISTS coffee_tea,
  DROP COLUMN IF EXISTS fun_fact;

ALTER TABLE users
  DROP COLUMN IF EXISTS avatar_url;

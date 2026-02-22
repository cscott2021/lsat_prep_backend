-- Down migration: drop everything in reverse dependency order
DROP TABLE IF EXISTS user_bookmarks CASCADE;
DROP TABLE IF EXISTS achievements CASCADE;
DROP TABLE IF EXISTS nudges CASCADE;
DROP TABLE IF EXISTS friendships CASCADE;
DROP TABLE IF EXISTS xp_events CASCADE;
DROP TABLE IF EXISTS user_gamification CASCADE;
DROP TABLE IF EXISTS generation_queue CASCADE;
DROP TABLE IF EXISTS user_question_history CASCADE;
DROP TABLE IF EXISTS user_ability_scores CASCADE;
DROP TABLE IF EXISTS validation_logs CASCADE;
DROP TABLE IF EXISTS answer_choices CASCADE;
DROP TABLE IF EXISTS questions CASCADE;
DROP TABLE IF EXISTS rc_passages CASCADE;
DROP TABLE IF EXISTS question_batches CASCADE;
DROP TABLE IF EXISTS users CASCADE;

-- +goose Up
-- +goose NO TRANSACTION

-- Adds 'time' as a node_type value so the post-extraction step can build
-- a year/month/day hierarchy for nodes with occurred_at. Time-узлы — это
-- именованные периоды ("2025", "2025-03", "2025-03-12"), которые
-- связываются с обычными узлами через part_of: новый узел с occurred_at
-- получает part_of к day, day part_of к month, month part_of к year.
-- Это даёт навигацию по графу через ось времени без отдельной таблицы.
--
-- ALTER TYPE ... ADD VALUE нельзя выполнять в транзакции (ограничение
-- Postgres); поэтому миграция помечена `goose NO TRANSACTION`.
ALTER TYPE node_type ADD VALUE IF NOT EXISTS 'time';

-- Дедупликация time-узлов: один пользователь — один узел на каждый
-- период. title хранит каноническое имя ("2025", "2025-03", "2025-03-12"),
-- по нему и идёт уникальность. UNIQUE на partial-индексе чтобы не
-- мешать обычным узлам, у которых title может повторяться.
--
-- deleted_at IS NULL: time-узлы soft-delete'нуть нельзя по UX-смыслу,
-- но условие держим на случай если в будущем эта политика поменяется.
CREATE UNIQUE INDEX IF NOT EXISTS idx_nodes_time_dedup
    ON nodes (user_id, title)
    WHERE type = 'time' AND deleted_at IS NULL;

-- +goose Down
-- +goose NO TRANSACTION

DROP INDEX IF EXISTS idx_nodes_time_dedup;

-- Postgres не поддерживает удаление значения из enum'а напрямую.
-- Откатить можно только пересозданием enum'а с переносом данных —
-- это сложнее чем удаление одного значения, и не нужно в практике
-- (down-миграции для enum-расширений обычно no-op в проде).
-- В local можно `docker-compose down -v` чтобы пересоздать схему с нуля.
SELECT 1;

package postgres

import (
	"context"
	"embed"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

const MigrationAdvisoryLockKey = 8472910482

// RunMigrations thực thi các file migration theo thứ tự phiên bản với PostgreSQL Advisory Lock
// đảm bảo tuyệt đối an toàn khi nhiều node Gateway cùng khởi động đồng thời.
func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("pgxpool không được để nil")
	}

	// 1. Chiếm advisory lock trên Postgres để tránh race condition giữa các node
	log.Println("[PostgreSQL Migrator] Đang chiếm PostgreSQL advisory lock để chuẩn bị kiểm tra schema...")
	_, err := pool.Exec(ctx, "SELECT pg_advisory_lock($1);", MigrationAdvisoryLockKey)
	if err != nil {
		return fmt.Errorf("không thể chiếm PostgreSQL advisory lock cho migration: %w", err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), "SELECT pg_advisory_unlock($1);", MigrationAdvisoryLockKey)
		log.Println("[PostgreSQL Migrator] Đã giải phóng PostgreSQL advisory lock.")
	}()

	// 2. Khởi tạo bảng lưu lịch sử schema_migrations nếu chưa có
	createSchemaMigrations := `
	CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		name VARCHAR(255) NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);`
	if _, err := pool.Exec(ctx, createSchemaMigrations); err != nil {
		return fmt.Errorf("lỗi khởi tạo bảng schema_migrations: %w", err)
	}

	// 3. Đọc danh sách phiên bản đã áp dụng
	rows, err := pool.Query(ctx, "SELECT version FROM schema_migrations ORDER BY version ASC;")
	if err != nil {
		return fmt.Errorf("lỗi truy vấn lịch sử schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[int]bool)
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return err
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// 4. Đọc danh sách các file SQL được nhúng trong binary
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("lỗi đọc thư mục migrations nhúng: %w", err)
	}

	type migrationFile struct {
		version  int
		fileName string
	}
	var files []migrationFile

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		parts := strings.SplitN(entry.Name(), "_", 2)
		if len(parts) < 2 {
			continue
		}
		v, parseErr := strconv.Atoi(parts[0])
		if parseErr != nil {
			continue
		}
		files = append(files, migrationFile{
			version:  v,
			fileName: entry.Name(),
		})
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].version < files[j].version
	})

	// 5. Chạy từng file migration chưa được áp dụng bên trong transaction
	appliedCount := 0
	for _, f := range files {
		if applied[f.version] {
			continue
		}

		log.Printf("[PostgreSQL Migrator] Đang áp dụng migration v%03d (%s)...", f.version, f.fileName)
		content, readErr := migrationFS.ReadFile("migrations/" + f.fileName)
		if readErr != nil {
			return fmt.Errorf("không thể đọc file migration nhúng %s: %w", f.fileName, readErr)
		}

		tx, txErr := pool.Begin(ctx)
		if txErr != nil {
			return fmt.Errorf("lỗi bắt đầu transaction cho migration %s: %w", f.fileName, txErr)
		}

		if _, execErr := tx.Exec(ctx, string(content)); execErr != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("lỗi thực thi DDL migration %s: %w", f.fileName, execErr)
		}

		insertSQL := "INSERT INTO schema_migrations (version, name, applied_at) VALUES ($1, $2, NOW());"
		if _, insErr := tx.Exec(ctx, insertSQL, f.version, f.fileName); insErr != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("lỗi ghi nhận schema_migrations %s: %w", f.fileName, insErr)
		}

		if commitErr := tx.Commit(ctx); commitErr != nil {
			return fmt.Errorf("lỗi commit transaction migration %s: %w", f.fileName, commitErr)
		}

		log.Printf("[PostgreSQL Migrator] Áp dụng thành công migration v%03d (%s).", f.version, f.fileName)
		appliedCount++
	}

	if appliedCount == 0 {
		log.Println("[PostgreSQL Migrator] Cơ sở dữ liệu đã ở trạng thái mới nhất, không có migration mới.")
	} else {
		log.Printf("[PostgreSQL Migrator] Hoàn tất áp dụng %d migration thành công.", appliedCount)
	}

	return nil
}

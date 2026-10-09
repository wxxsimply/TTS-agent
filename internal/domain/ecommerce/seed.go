package ecommerce

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tts-agent/internal/domain"
)

var (
	regions       = []string{"US", "EU", "UK", "SEA", "JP"}
	channels      = []string{"organic", "facebook", "google", "tiktok", "direct"}
	categories    = []string{"Electronics", "Fashion", "Home", "Beauty"}
	platforms     = []string{"facebook", "google", "tiktok"}
	statuses      = []string{"paid", "shipped", "delivered", "refunded", "cancelled"}
	statusWeights = []int{50, 20, 15, 10, 5}
)

func Seed(root, dsn string) error {
	_ = root
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()

	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return fmt.Errorf("check users: %w", err)
	}
	if count > 0 {
		fmt.Println("Seed skipped: data already exists.")
		return nil
	}

	rng := rand.New(rand.NewSource(42))
	baseDate := time.Now().AddDate(0, 0, -90)

	for i := 0; i < 800; i++ {
		_, err := pool.Exec(ctx,
			`INSERT INTO users (email, name, region, channel, registered_at) VALUES ($1,$2,$3,$4,$5)`,
			fmt.Sprintf("user%d@example.com", i), fmt.Sprintf("Customer %d", i),
			regions[rng.Intn(len(regions))], channels[rng.Intn(len(channels))],
			baseDate.Add(time.Duration(rng.Intn(60*24))*time.Hour),
		)
		if err != nil {
			return err
		}
	}

	rows, err := pool.Query(ctx, `SELECT id FROM users`)
	if err != nil {
		return err
	}
	var userIDs []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		userIDs = append(userIDs, id)
	}
	rows.Close()

	type product struct {
		id    int
		price float64
	}
	var products []product
	for i := 0; i < 120; i++ {
		var id int
		var price float64
		err := pool.QueryRow(ctx,
			`INSERT INTO products (sku, name, category, price) VALUES ($1,$2,$3,$4) RETURNING id, price`,
			fmt.Sprintf("SKU-%04d", i), fmt.Sprintf("Product %d", i),
			categories[rng.Intn(len(categories))], 9.99+rng.Float64()*290,
		).Scan(&id, &price)
		if err != nil {
			return err
		}
		products = append(products, product{id, price})
	}

	var campaignIDs []int
	for _, platform := range platforms {
		var id int
		err := pool.QueryRow(ctx,
			`INSERT INTO ad_campaigns (name, platform, start_date, end_date) VALUES ($1,$2,$3,$4) RETURNING id`,
			platform+" Q1 Campaign", platform, baseDate.Format("2006-01-02"), baseDate.AddDate(0, 0, 90).Format("2006-01-02"),
		).Scan(&id)
		if err != nil {
			return err
		}
		campaignIDs = append(campaignIDs, id)
	}

	for _, cid := range campaignIDs {
		for day := 0; day < 90; day++ {
			d := baseDate.AddDate(0, 0, day)
			_, err := pool.Exec(ctx,
				`INSERT INTO ad_spend (campaign_id, spend_date, amount, impressions, clicks) VALUES ($1,$2,$3,$4,$5)`,
				cid, d.Format("2006-01-02"), 50+rng.Float64()*450, 1000+rng.Intn(49000), 50+rng.Intn(1950),
			)
			if err != nil {
				return err
			}
		}
	}

	repeatUsers := make([]int, 0, len(userIDs)*3/10)
	for _, id := range userIDs[:len(userIDs)*3/10] {
		repeatUsers = append(repeatUsers, id)
	}

	for i := 0; i < 5000; i++ {
		var uid int
		if rng.Float64() < 0.6 && len(repeatUsers) > 0 {
			uid = repeatUsers[rng.Intn(len(repeatUsers))]
		} else {
			uid = userIDs[rng.Intn(len(userIDs))]
		}
		created := baseDate.Add(time.Duration(rng.Intn(90*24)) * time.Hour)
		status := weightedChoice(rng, statuses, statusWeights)
		nItems := 1 + rng.Intn(3)
		var total float64
		for j := 0; j < nItems; j++ {
			p := products[rng.Intn(len(products))]
			total += p.price * float64(1+rng.Intn(3))
		}
		var orderID int
		err := pool.QueryRow(ctx,
			`INSERT INTO orders (user_id, order_no, status, total_amount, created_at) VALUES ($1,$2,$3,$4,$5) RETURNING id`,
			uid, fmt.Sprintf("ORD-%06d", i), status, total, created,
		).Scan(&orderID)
		if err != nil {
			return err
		}
		for j := 0; j < nItems; j++ {
			p := products[rng.Intn(len(products))]
			_, err := pool.Exec(ctx,
				`INSERT INTO order_items (order_id, product_id, quantity, unit_price) VALUES ($1,$2,$3,$4)`,
				orderID, p.id, 1+rng.Intn(3), p.price,
			)
			if err != nil {
				return err
			}
		}
	}

	for i := 0; i < 8000; i++ {
		src := channels[rng.Intn(len(channels))]
		visited := baseDate.Add(time.Duration(rng.Intn(90*24)) * time.Hour)
		converted := rng.Float64() < 0.08
		var uid *int
		if converted {
			id := userIDs[rng.Intn(len(userIDs))]
			uid = &id
		}
		_, err := pool.Exec(ctx,
			`INSERT INTO traffic_sessions (session_id, source, landing_page, visited_at, converted, user_id) VALUES ($1,$2,$3,$4,$5,$6)`,
			fmt.Sprintf("sess-%06d", i), src, fmt.Sprintf("/products/%d", 1+rng.Intn(120)), visited, converted, uid,
		)
		if err != nil {
			return err
		}
	}

	_, _ = pool.Exec(ctx, `GRANT SELECT ON ALL TABLES IN SCHEMA public TO agent_readonly`)
	_, _ = pool.Exec(ctx, `GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO agent_readonly`)

	fmt.Println("E-commerce seed data loaded successfully.")
	return nil
}

func weightedChoice(rng *rand.Rand, items []string, weights []int) string {
	total := 0
	for _, w := range weights {
		total += w
	}
	n := rng.Intn(total)
	for i, w := range weights {
		n -= w
		if n < 0 {
			return items[i]
		}
	}
	return items[0]
}

func InitRegistry(root string) {
	domain.Register(&domain.Plugin{
		Name:           "ecommerce",
		DDLPath:        domain.ResolvePath(root, "ecommerce", "ddl.sql"),
		MetadataPath:   domain.ResolvePath(root, "ecommerce", "metadata.yaml"),
		DictionaryPath: domain.ResolvePath(root, "ecommerce", "dictionary.yaml"),
		FewshotDir:     domain.ResolvePath(root, "ecommerce", "fewshot"),
		SeedFn:         Seed,
	})
}

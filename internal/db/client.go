package db

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
	"github.com/paddlesteamer/hdn-drv/internal/common"
)

type Client struct {
	db *sql.DB
}

var tableSchemas = [...]string{
	`CREATE TABLE files (
		"inode"  INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
		"name"   TEXT NOT NULL,
		"url"    TEXT NOT NULL DEFAULT "",
		"size"   INTEGER NOT NULL DEFAULT 0,
		"mode"   INTEGER NOT NULL,
		"parent" INTEGER NOT NULL,
		"type"   INTEGER NOT NULL,
		UNIQUE("name", "parent"),
		FOREIGN KEY("parent") REFERENCES folders("id")
	);`,
	fmt.Sprintf(`INSERT INTO files(inode, name, mode, parent, type) VALUES (1, "", 493, 0, %d);`, common.DRV_FOLDER), // root folder with mode 0755
}

// InitDB initializes tables. Supposed to be called on the very first run.
func InitDB(path string) error {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return fmt.Errorf("couldn't open db at %s: %v", path, err)
	}
	defer db.Close()

	for _, sqlStr := range tableSchemas {
		st, err := db.Prepare(sqlStr)
		if err != nil {
			return fmt.Errorf("error in query `%s`: %v", sqlStr, err)
		}

		_, err = st.Exec()
		if err != nil {
			return fmt.Errorf("couldn't execute initialization query: %v", err)
		}
	}

	return nil
}

// NewClient returns a new database connection.
func NewClient(path string) (*Client, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("couldn't open db at %s: %v", path, err)
	}

	c := &Client{
		db: db,
	}
	return c, nil
}

// Close terminates database connection.
func (c *Client) Close() {
	c.db.Close()
}

func (c *Client) Search(parent int64, name string) (*common.Metadata, error) {
	query, err := c.db.Prepare("SELECT * FROM files WHERE name=? and parent=?")
	if err != nil {
		return nil, fmt.Errorf("couldn't prepare statement: %v", err)
	}

	row, err := query.Query(name, parent)
	if err != nil {
		return nil, fmt.Errorf("there is an error in query: %v", err)
	}
	defer row.Close()

	if !row.Next() {
		return nil, common.ErrNotFound
	}

	return c.parseRow(row)
}

func (c *Client) Get(inode int64) (*common.Metadata, error) {
	query, err := c.db.Prepare("SELECT * FROM files WHERE inode=?")
	if err != nil {
		return nil, fmt.Errorf("couldn't prepare statement: %v", err)
	}

	row, err := query.Query(inode)
	if err != nil {
		return nil, fmt.Errorf("there is an error in query: %v", err)
	}
	defer row.Close()

	if !row.Next() {
		return nil, common.ErrNotFound
	}

	md, err := c.parseRow(row)
	if err != nil {
		return nil, err
	}

	err = c.fillNLink(md)
	if err != nil {
		return nil, fmt.Errorf("couldn't get nlink count: %v", err)
	}

	return md, nil
}

func (c *Client) Delete(inode int64) error {
	query, err := c.db.Prepare("DELETE FROM files WHERE inode=?")
	if err != nil {
		return fmt.Errorf("couldn't prepare statement: %v", err)
	}

	_, err = query.Exec(inode)
	if err != nil {
		return fmt.Errorf("couldn't delete entry: %v", err)
	}

	return nil
}

func (c *Client) GetChildren(parent int64) ([]common.Metadata, error) {
	query, err := c.db.Prepare("SELECT * FROM files WHERE parent=?")
	if err != nil {
		return nil, fmt.Errorf("couldn't prepare statement: %v", err)
	}

	row, err := query.Query(parent)
	if err != nil {
		return nil, fmt.Errorf("there is an error in query: %v", err)
	}
	defer row.Close()

	mdList := []common.Metadata{}
	for row.Next() {
		md, err := c.parseRow(row)
		if err != nil {
			return nil, err
		}

		err = c.fillNLink(md)
		if err != nil {
			return nil, fmt.Errorf("couldn't get nlink count: %v", err)
		}

		mdList = append(mdList, *md)
	}

	return mdList, nil
}

func (c *Client) DeleteChildren(parent int64) error {
	query, err := c.db.Prepare("DELETE FROM files WHERE parent=?")
	if err != nil {
		return fmt.Errorf("couldn't prepare statement: %v", err)
	}

	_, err = query.Exec(parent)
	if err != nil {
		return fmt.Errorf("couldn't delete children: %v", err)
	}

	return nil
}

func (c *Client) AddDirectory(parent int64, name string, mode int) (*common.Metadata, error) {
	query, err := c.db.Prepare("INSERT INTO files(name, mode, parent, type) VALUES(?, ?, ?, ?)")
	if err != nil {
		return nil, fmt.Errorf("couldn't prepare statement: %v", err)
	}

	_, err = query.Exec(name, mode, parent, common.DRV_FOLDER)
	if err != nil {
		return nil, fmt.Errorf("couldn't insert directory: %v", err)
	}

	query, err = c.db.Prepare("SELECT * FROM files WHERE name=? and parent=?")
	if err != nil {
		return nil, fmt.Errorf("couldn't prepare statement: %v", err)
	}

	row, err := query.Query(name, parent)
	if err != nil {
		return nil, fmt.Errorf("there is an error in query: %v", err)
	}
	defer row.Close()

	if !row.Next() {
		return nil, fmt.Errorf("row should be inserted but apparently it didn't")
	}

	md, err := c.parseRow(row)
	if err != nil {
		return nil, err
	}

	// since the directory has just been created, there are only '.' and '..'
	md.NLink = 2
	return md, nil
}

func (c *Client) CreateFile(parent int64, name string, mode int, url string) (*common.Metadata, error) {
	query, err := c.db.Prepare("INSERT INTO files(name, url, size, mode, parent, type) VALUES(?, ?, ?, ?, ?, ?)")
	if err != nil {
		return nil, fmt.Errorf("couldn't prepare statement: %v", err)
	}

	_, err = query.Exec(name, url, 0, mode, parent, common.DRV_FILE)
	if err != nil {
		return nil, fmt.Errorf("couldn't insert directory: %v", err)
	}

	query, err = c.db.Prepare("SELECT * FROM files WHERE name=? and parent=?")
	if err != nil {
		return nil, fmt.Errorf("couldn't prepare statement: %v", err)
	}

	row, err := query.Query(name, parent)
	if err != nil {
		return nil, fmt.Errorf("there is an error in query: %v", err)
	}
	defer row.Close()

	if !row.Next() {
		return nil, fmt.Errorf("row should be inserted but apparently it didn't")
	}

	md, err := c.parseRow(row)
	if err != nil {
		return nil, err
	}

	// it's file and hardlink isn't supported
	md.NLink = 1
	return md, nil
}

func (c *Client) Update(md *common.Metadata) error {
	query, err := c.db.Prepare("UPDATE files SET name=?, url=?, size=?, mode=?, parent=?, type=? WHERE inode=?")
	if err != nil {
		return fmt.Errorf("couldn't prepare statement: %v", err)
	}

	_, err = query.Exec(md.Name, md.URL, md.Size, md.Mode, md.Parent, md.Type, md.Inode)
	if err != nil {
		return fmt.Errorf("couldn't update file: %v", err)
	}

	return nil
}

func (c *Client) fillNLink(md *common.Metadata) error {
	if md.Type == common.DRV_FILE {
		md.NLink = 1
		return nil
	}

	query, err := c.db.Prepare("SELECT COUNT(*) FROM files WHERE parent=? and type=?")
	if err != nil {
		return fmt.Errorf("couldn't prepare statement: %v", err)
	}

	row, err := query.Query(md.Inode, common.DRV_FOLDER)
	if err != nil {
		return fmt.Errorf("there is an error in query: %v", err)
	}
	defer row.Close()

	row.Next() // should always be true

	var count int

	err = row.Scan(&count)
	if err != nil {
		return fmt.Errorf("couldn't parse row count")
	}

	md.NLink = count + 2 // don't forget '.' and '..' dirs
	return nil
}

func (c *Client) parseRow(row *sql.Rows) (*common.Metadata, error) {
	md := &common.Metadata{}
	err := row.Scan(&md.Inode, &md.Name, &md.URL, &md.Size, &md.Mode, &md.Parent, &md.Type)
	if err != nil {
		return nil, fmt.Errorf("couldn't parse row: %v", err)
	}

	return md, nil
}

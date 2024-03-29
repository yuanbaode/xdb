package main

import (
	"bytes"
	"database/sql"
	"flag"
	"fmt"
	_ "github.com/go-sql-driver/mysql"
	"go/format"
	"log"
	"os"
	"path"
	"regexp"
	"strings"
	"text/template"
)

const structTemplate = `
package {{.Package}}



// {{.StructName}} struct is a row record of the {{.TableName}} table in the {{.DatabaseName}} database
type {{.StructName}} struct {
    {{range .Columns}}{{.CamelCase | title}} {{.Type}} ` + "`gorm:\"{{.GormTag}}\" " + "json:\"{{.JsonTag}}\"`" + `{{if .Comment}}// {{.Comment}}{{end}} 
    {{end}}
}

const tableName{{.StructName}} = "{{.TableName}}"
// TableName sets the insert table name for this struct type
func (p {{.StructName}}) TableName() string {
	return tableName{{.StructName}}
}
`

func main() {
	var (
		datasource string
		tableName  string
		database   string
		dir        string
		model      string
	)

	flag.StringVar(&datasource, "datasource", "root:@tcp(localhost:3306)/test", "MySQL datasource string")
	flag.StringVar(&tableName, "table", "", "MySQL table name")
	flag.StringVar(&database, "database", "", "MySQL database name")
	flag.StringVar(&dir, "dir", "", "MySQL database name")
	flag.StringVar(&model, "model", "model", "package name")

	flag.Parse()
	db, err := sql.Open("mysql", datasource)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	var tables []string
	if tableName == "" {
		tables, err = getTables(db, database)
		if err != nil {
			log.Fatal(err)
		}

	} else {
		tables = append(tables, tableName)
	}
	cnt := 0
	for _, table := range tables {
		generateStruct(db, table, database, dir, model)
		cnt++
	}
	log.Printf("over! total %d", cnt)
}
func getTables(db *sql.DB, dataBase string) (tables []string, err error) {
	log.Printf(`get tables from %s`, dataBase)
	rows, err := db.Query(`SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA= ?`, dataBase)
	if err != nil {
		log.Println(err)
		return
	}
	for rows.Next() {
		var table string
		err = rows.Scan(&table)
		if err != nil {
			log.Println(err)
			return
		}
		tables = append(tables, table)

	}
	return
}

type Column struct {
	Field   string
	Type    string
	Null    string
	Key     string
	Default sql.NullString // 使用 sql.NullString 表示可能为 NULL 的字段
	Extra   string
	Comment string
	GormTag string
}

func generateStruct(db *sql.DB, tableName, database, dir, packageName string) (err error) {
	log.Printf(`gen %s`, tableName)

	rows, err := db.Query(`SELECT COLUMN_NAME,DATA_TYPE,IS_NULLABLE,COLUMN_KEY,COLUMN_DEFAULT,EXTRA,COLUMN_COMMENT FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA =? AND TABLE_NAME =?`, database, tableName)
	if err != nil {
		log.Fatal(err)
	}

	var columns []Column
	for rows.Next() {
		var column Column
		if err := rows.Scan(&column.Field, &column.Type, &column.Null, &column.Key, &column.Default, &column.Extra, &column.Comment); err != nil {
			log.Fatal(err)
		}
		columns = append(columns, column)
	}

	rows.Close()
	//for i, _ := range columns {
	//	columns[i].Comment = getColumnComment(db, tableName, database, columns[i].Field)
	//}
	structFields := make([]StructField, 0, len(columns))
	for _, column := range columns {
		structFields = append(structFields, column.GetStructField())
	}
	tb := Struct{
		DatabaseName: database,
		StructName:   toCamelCase(tableName),
		TableName:    tableName,
		Columns:      structFields,
		Package:      packageName,
		Dir:          dir,
	}
	_ = tb
	//generateFile(tableName, packageName, dir, columns)
	generateFileWithTmpl(tb)
	return
}

func generateFileWithTmpl(tb Struct) {
	fileName := tb.TableName + ".go"
	if tb.Dir != "" {
		if _, err := os.Stat(tb.Dir); os.IsNotExist(err) {
			if err := os.MkdirAll(tb.Dir, 0755); err != nil {
				log.Fatalf("Error creating output directory: %v", err)
			}
		}
		fileName = path.Join(tb.Dir, fileName)
	}

	file, err := os.Create(fileName)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()
	tmpl, err := template.New("structTemplate").Funcs(template.FuncMap{
		"title": strings.Title,
	}).Parse(structTemplate)
	if err != nil {
		log.Fatal(err)
	}
	buffer := bytes.NewBuffer([]byte{})
	err = tmpl.Execute(buffer, tb)
	if err != nil {
		log.Fatal(err)
	}
	source, _ := format.Source(buffer.Bytes())
	file.Write(source)
}
func getColumnComment(db *sql.DB, tableName, database, columnName string) string {
	var columnComment sql.NullString
	err := db.QueryRow("SELECT COLUMN_COMMENT FROM information_schema.COLUMNS WHERE TABLE_NAME = ? AND COLUMN_NAME = ? AND TABLE_SCHEMA =?",
		tableName, columnName, database).Scan(&columnComment)
	if err != nil {
		log.Fatal("Error getting column comment:", err)
		return ""
	}
	return columnComment.String
}

type Struct struct {
	DatabaseName string
	StructName   string
	TableName    string
	Columns      []StructField
	Package      string
	Dir          string
}

func (c *Column) GetStructField() StructField {
	sf := StructField{
		CamelCase: toCamelCase(c.Field),
		Field:     c.Field,
		Type:      mysqlTypeToGoType(c.Type, c.Null),
		Comment:   c.Comment,
		GormTag:   c.getGormTag(),
		JsonTag:   CamelCaseToUnderscore(c.Field),
	}
	return sf
}

type StructField struct {
	Field     string
	CamelCase string
	Type      string
	Comment   string
	GormTag   string
	JsonTag   string
}

// getGormTag 用于生成gorm tag
func (c *Column) getGormTag() string {
	tag := fmt.Sprintf("column:%s", c.Field)
	if c.Extra == "auto_increment" {
		tag += ";AUTO_INCREMENT"
	}
	if c.Key == "PRI" {
		tag += ";primaryKey"
	}
	if c.Default.Valid && c.Default.String != "" {
		tag += ";default:" + c.Default.String
	}
	if c.Null == "NO" && c.Key != "PRI" {
		tag += ";NOT NULL"
	}

	return tag
}

func generateGoStruct(tableName, packageName string, columns []Column) string {

	buf := bytes.Buffer{}
	buf.WriteString(`package ` + packageName + "\n")
	structName := toCamelCase(tableName)
	buf.WriteString(fmt.Sprintf("type %s struct {\n", structName))
	for _, column := range columns {
		goType := mysqlTypeToGoType(column.Type, column.Null)
		fieldName := toCamelCase(column.Field)

		// 如果字段允许为 NULL 且默认值为 NULL，则使用 sql.NullString 类型
		//if column.Null == "YES" && column.Default.String == "NULL" {
		//	buf.WriteString(fmt.Sprintf("\t%s %s `gorm:\"%s\" json:\"%s\"`\n", fieldName, "sql.NullString", column.Field, column.Field))
		//} else {
		//if column.Key
		buf.WriteString(fmt.Sprintf("\t%s %s `gorm:\"%s\" json:\"%s\"`", fieldName, goType, column.getGormTag(), column.Field))
		if column.Comment != "" {
			buf.WriteString(fmt.Sprintf(" // %s\n", column.Comment))
		} else {
			buf.WriteString("\n")

		}
		//}
	}
	buf.WriteString("}\n")
	buf.WriteString(fmt.Sprintf("const tableName%s = \"%s\"\n", structName, tableName))
	buf.WriteString(fmt.Sprintf("func (p %s) TableName() string {\n\treturn tableName%s\n}", structName, structName))
	source, _ := format.Source(buf.Bytes())
	return string(source)
}

func mysqlTypeToGoType(mysqlType, isNullable string) string {
	isNullable = "FALSE"
	index := strings.Index(mysqlType, "(")
	if index > 0 {
		mysqlType = mysqlType[0:index]
	}

	switch mysqlType {
	case "tinyint", "smallint", "mediumint":
		if isNullable == "YES" {
			return "sql.NullInt64"
		}
		return "int8"
	case "bigint", "int":
		if isNullable == "YES" {
			return "sql.NullInt64"
		}
		return "int64"
	case "float", "double", "decimal":
		if isNullable == "YES" {
			return "sql.NullFloat64"
		}
		return "float64"
	case "char", "varchar", "enum", "set", "text", "mediumtext", "longtext":
		if isNullable == "YES" {
			return "sql.NullString"
		}
		return "string"
	case "date", "datetime", "timestamp":
		if isNullable == "YES" {
			return "sql.NullTime"
		}
		return "time.Time"
	default:
		return "interface{}"
	}
}

func toCamelCase(s string) string {
	parts := strings.Split(s, "_")
	for i := 0; i < len(parts); i++ {
		parts[i] = strings.Title(parts[i])
	}
	return strings.Join(parts, "")
}

// CamelCaseToUnderscore 将驼峰命名的字符串转换为下划线命名
func CamelCaseToUnderscore(s string) string {
	var words []string

	// 使用正则表达式将字符串拆分为单词
	re := regexp.MustCompile("[a-z0-9]+|[A-Z][a-z0-9]*")
	matches := re.FindAllString(s, -1)

	// 将每个单词转换为小写，并添加到切片中
	for _, match := range matches {
		words = append(words, strings.ToLower(match))
	}

	// 将切片中的单词用下划线连接起来
	result := strings.Join(words, "_")

	return result
}

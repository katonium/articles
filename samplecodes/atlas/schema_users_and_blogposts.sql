-- Create "users" table
CREATE TABLE `users` (`id` int NOT NULL, `name` varchar(100) NULL, PRIMARY KEY (`id`)) CHARSET utf8mb4 COLLATE utf8mb4_0900_ai_ci;

-- create "blog_posts" table
CREATE TABLE `blog_posts` (
  `id` int NOT NULL,
  `title` varchar(100) NULL,
  `body` text NULL,
  `author_id` int NULL,
  PRIMARY KEY (`id`),
  CONSTRAINT `author_fk` FOREIGN KEY (`author_id`) REFERENCES `example`.`users` (`id`)
);
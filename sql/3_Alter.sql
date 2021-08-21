ALTER TABLE `isu_condition` ADD (
  `is_dirty` TINYINT(1) AS (`condition` REGEXP 'is_dirty=true') STORED NOT NULL,
  `is_overweight` TINYINT(1) AS (`condition` REGEXP 'is_overweight=true') STORED NOT NULL,
  `is_broken` TINYINT(1) AS (`condition` REGEXP 'is_broken=true') STORED NOT NULL,
  `condition_level` TINYINT(1) AS (`is_dirty`+`is_overweight`+`is_broken`) STORED NOT NULL
);

/*
 * SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

use std::cmp::min;
use std::fmt;
use std::str::FromStr;

use serde::{Deserialize, Deserializer, Serialize, de};

pub const DEFAULT_PAGE_RECORD_LIMIT: usize = 50;

/// Serde deserialization decorator to map empty Strings to None.
pub fn empty_string_as_none<'de, D, T>(de: D) -> Result<Option<T>, D::Error>
where
    D: Deserializer<'de>,
    T: FromStr,
    T::Err: fmt::Display,
{
    let opt = Option::<String>::deserialize(de)?;
    match opt.as_deref() {
        None | Some("") => Ok(None),
        Some(s) => FromStr::from_str(s).map_err(de::Error::custom).map(Some),
    }
}

#[derive(Deserialize, Debug, Default)]
pub struct PaginationParams {
    #[serde(default, deserialize_with = "empty_string_as_none")]
    pub limit: Option<usize>,
    #[serde(default, deserialize_with = "empty_string_as_none")]
    pub current_page: Option<usize>,
}

pub struct PaginationInfo {
    pub current_page: usize,
    pub previous: usize,
    pub next: usize,
    pub pages: usize,
    pub page_range_start: usize,
    pub page_range_end: usize,
    pub limit: usize,
    pub total_items: usize,
}

#[derive(Serialize)]
pub struct PaginatedResponse<T: Serialize> {
    pub items: T,
    pub current_page: usize,
    pub total_items: usize,
    pub total_pages: usize,
    pub limit: usize,
}

/// Resolve raw pagination params into a concrete `current_page` and `limit`.
/// A `limit` of `0` means "return all items" (backward compatibility).
fn resolve_params(params: &PaginationParams) -> (usize, usize) {
    let current_page = params.current_page.unwrap_or(0);
    let limit = params.limit.map_or(DEFAULT_PAGE_RECORD_LIMIT, |l| {
        min(l, DEFAULT_PAGE_RECORD_LIMIT)
    });
    (current_page, limit)
}

fn compute_pagination_info(
    total_items: usize,
    current_page: usize,
    limit: usize,
) -> PaginationInfo {
    let pages = if limit == 0 {
        if total_items == 0 { 0 } else { 1 }
    } else {
        total_items.div_ceil(limit)
    };

    PaginationInfo {
        current_page,
        previous: current_page.saturating_sub(1),
        next: current_page.saturating_add(1),
        pages,
        page_range_start: current_page.saturating_sub(3),
        page_range_end: min(current_page.saturating_add(4), pages),
        limit,
        total_items,
    }
}

/// Paginate a slice of IDs. Returns pagination metadata and the IDs for the
/// requested page. The caller should then batch-fetch details for only these IDs.
pub fn paginate_ids<T: Clone>(
    all_ids: &[T],
    params: &PaginationParams,
) -> (PaginationInfo, Vec<T>) {
    let (current_page, limit) = resolve_params(params);
    let total_items = all_ids.len();

    if limit == 0 {
        let info = compute_pagination_info(total_items, 0, 0);
        return (info, all_ids.to_vec());
    }

    let info = compute_pagination_info(total_items, current_page, limit);

    let offset = current_page.saturating_mul(limit);
    if offset >= total_items {
        return (info, vec![]);
    }

    let page_ids: Vec<T> = all_ids.iter().skip(offset).take(limit).cloned().collect();
    (info, page_ids)
}

/// Paginate an already-collected Vec (e.g. after in-memory filtering).
/// Drains elements outside the page window so only the current page remains.
pub fn paginate_vec<T>(items: Vec<T>, params: &PaginationParams) -> (PaginationInfo, Vec<T>) {
    let (current_page, limit) = resolve_params(params);
    let total_items = items.len();

    if limit == 0 {
        let info = compute_pagination_info(total_items, 0, 0);
        return (info, items);
    }

    let info = compute_pagination_info(total_items, current_page, limit);

    let offset = current_page.saturating_mul(limit);
    if offset >= total_items {
        return (info, vec![]);
    }

    let page_items: Vec<T> = items.into_iter().skip(offset).take(limit).collect();
    (info, page_items)
}

impl PaginationInfo {
    pub fn to_response<T: Serialize>(&self, items: T) -> PaginatedResponse<T> {
        PaginatedResponse {
            items,
            current_page: self.current_page,
            total_items: self.total_items,
            total_pages: self.pages,
            limit: self.limit,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const LIMIT: usize = DEFAULT_PAGE_RECORD_LIMIT;

    #[test]
    fn paginate_ids_first_page() {
        let ids: Vec<i32> = (0..250).collect();
        let params = PaginationParams {
            current_page: Some(0),
            limit: Some(LIMIT),
        };
        let (info, page) = paginate_ids(&ids, &params);
        assert_eq!(page.len(), LIMIT);
        assert_eq!(page[0], 0);
        assert_eq!(info.pages, 5);
        assert_eq!(info.total_items, 250);
    }

    #[test]
    fn paginate_ids_last_page() {
        let ids: Vec<i32> = (0..250).collect();
        let params = PaginationParams {
            current_page: Some(4),
            limit: Some(LIMIT),
        };
        let (info, page) = paginate_ids(&ids, &params);
        assert_eq!(page.len(), 50);
        assert_eq!(page[0], 200);
        assert_eq!(info.pages, 5);
    }

    #[test]
    fn paginate_ids_beyond_range() {
        let ids: Vec<i32> = (0..250).collect();
        let params = PaginationParams {
            current_page: Some(10),
            limit: Some(LIMIT),
        };
        let (info, page) = paginate_ids(&ids, &params);
        assert!(page.is_empty());
        assert_eq!(info.pages, 5);
        assert_eq!(info.total_items, 250);
    }

    #[test]
    fn paginate_ids_limit_zero_returns_all() {
        let ids: Vec<i32> = (0..250).collect();
        let params = PaginationParams {
            current_page: None,
            limit: Some(0),
        };
        let (info, page) = paginate_ids(&ids, &params);
        assert_eq!(page.len(), 250);
        assert_eq!(info.pages, 1);
        assert_eq!(info.total_items, 250);
    }

    #[test]
    fn paginate_ids_defaults() {
        let ids: Vec<i32> = (0..250).collect();
        let params = PaginationParams {
            current_page: None,
            limit: None,
        };
        let (info, page) = paginate_ids(&ids, &params);
        assert_eq!(page.len(), LIMIT);
        assert_eq!(info.pages, 5);
        assert_eq!(info.limit, LIMIT);
        assert_eq!(info.current_page, 0);
    }

    #[test]
    fn paginate_vec_with_filter() {
        let items: Vec<i32> = (0..250).collect();
        let params = PaginationParams {
            current_page: Some(1),
            limit: Some(LIMIT),
        };
        let (info, page) = paginate_vec(items, &params);
        assert_eq!(page.len(), LIMIT);
        assert_eq!(page[0], 50);
        assert_eq!(info.pages, 5);
        assert_eq!(info.total_items, 250);
    }

    #[test]
    fn paginate_empty() {
        let ids: Vec<i32> = vec![];
        let params = PaginationParams {
            current_page: None,
            limit: None,
        };
        let (info, page) = paginate_ids(&ids, &params);
        assert!(page.is_empty());
        assert_eq!(info.pages, 0);
        assert_eq!(info.total_items, 0);
    }
}

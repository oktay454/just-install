// just-install - The simple package installer for Windows
// Copyright (C) 2020 just-install authors.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, version 3 of the License.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

package strings2

import "strings"

// IsEmpty returns whether the given string is empty after trimming it for spaces.
func IsEmpty(s string) bool {
	return !IsNotEmpty(s)
}

// IsNotEmpty returns whether the given string is not empty after trimming it for spaces.
func IsNotEmpty(s string) bool {
	return len(strings.TrimSpace(s)) > 0
}

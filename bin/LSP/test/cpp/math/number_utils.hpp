#pragma once

namespace fixture {

inline int clamp_to_range(int value, int lower, int upper) {
  if (value < lower) {
    return lower;
  }
  if (value > upper) {
    return upper;
  }
  return value;
}

}  // namespace fixture

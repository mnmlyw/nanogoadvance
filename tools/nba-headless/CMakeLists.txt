cmake_minimum_required(VERSION 3.24)
project(nba-headless LANGUAGES C CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

# Default to the project-local clone (gitignored at upstream/NanoBoyAdvance/).
# Override with -DNBA_SOURCE_DIR=/path/to/nanoboyadvance if needed.
if(NOT DEFINED NBA_SOURCE_DIR)
  set(NBA_SOURCE_DIR "${CMAKE_CURRENT_SOURCE_DIR}/../../upstream/NanoBoyAdvance"
      CACHE PATH "Path to NanoBoyAdvance source tree")
endif()

if(NOT EXISTS "${NBA_SOURCE_DIR}/CMakeLists.txt")
  message(FATAL_ERROR "NanoBoyAdvance source not found at ${NBA_SOURCE_DIR}. Set -DNBA_SOURCE_DIR=path.")
endif()

set(PLATFORM_QT OFF CACHE BOOL "" FORCE)
add_subdirectory(${NBA_SOURCE_DIR} nanoboyadvance_build EXCLUDE_FROM_ALL)

add_executable(nba-headless main.cc)
target_link_libraries(nba-headless PRIVATE nba platform-core)
target_include_directories(nba-headless PRIVATE
  ${NBA_SOURCE_DIR}/src/nba/include
  ${NBA_SOURCE_DIR}/src/platform/core/include
)

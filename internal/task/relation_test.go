package task_test

import (
	"testing"

	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

func TestParseRelationType(t *testing.T) {
	t.Parallel()

	t.Run("empty defaults to blocks", func(t *testing.T) {
		t.Parallel()
		got, err := task.ParseRelationType("")
		require.NoError(t, err)
		require.Equal(t, task.RelationBlocks, got)
	})

	t.Run("canonical values parse correctly", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			input string
			want  task.RelationType
		}{
			{"blocks", task.RelationBlocks},
			{"relates", task.RelationRelates},
			{"duplicates", task.RelationDuplicates},
			{"parent", task.RelationParent},
		}
		for _, c := range cases {
			c := c
			t.Run(c.input, func(t *testing.T) {
				t.Parallel()
				got, err := task.ParseRelationType(c.input)
				require.NoError(t, err)
				require.Equal(t, c.want, got)
			})
		}
	})

	t.Run("case and whitespace insensitive", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			input string
			want  task.RelationType
		}{
			{"BLOCKS", task.RelationBlocks},
			{"Relates", task.RelationRelates},
			{"  duplicates  ", task.RelationDuplicates},
			{"\tPARENT\n", task.RelationParent},
		}
		for _, c := range cases {
			c := c
			t.Run(c.input, func(t *testing.T) {
				t.Parallel()
				got, err := task.ParseRelationType(c.input)
				require.NoError(t, err)
				require.Equal(t, c.want, got)
			})
		}
	})

	t.Run("unknown value returns ErrInvalidRelation", func(t *testing.T) {
		t.Parallel()
		unknowns := []string{"depends", "child", "causes", "UNRELATED", "block s"}
		for _, s := range unknowns {
			s := s
			t.Run(s, func(t *testing.T) {
				t.Parallel()
				_, err := task.ParseRelationType(s)
				require.Error(t, err)
				require.ErrorIs(t, err, task.ErrInvalidRelation)
			})
		}
	})
}
